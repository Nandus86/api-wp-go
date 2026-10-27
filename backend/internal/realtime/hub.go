// Package realtime fans WhatsApp events out to WebSocket and SSE subscribers.
// With Redis configured, events are published on a channel so every replica of the API
// delivers them to its own subscribers; without Redis the hub is process-local.
package realtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/internal/service/webhook"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.uber.org/zap"
)

const redisChannel = "wmb:events"

// Event is what subscribers receive.
type Event struct {
	Event      string          `json:"event"`
	InstanceID string          `json:"instance_id"`
	Timestamp  int64           `json:"timestamp"`
	Data       json.RawMessage `json:"data" swaggertype:"object"`
}

// Filter selects which events a subscriber gets.
type Filter struct {
	// Instances limits delivery to these instance ids (nil = every instance).
	Instances map[string]bool
	// Events limits delivery to these event names (empty = all). UAZAPI aliases accepted.
	Events []string
	// ExcludeMessages applies UAZAPI message filters (fromMeYes, isGroupYes...).
	ExcludeMessages []string
}

func (f Filter) match(e *Event) bool {
	if f.Instances != nil && !f.Instances[e.InstanceID] {
		return false
	}
	return webhook.Matches(f.Events, f.ExcludeMessages, e.Event, e.Data)
}

type Subscriber struct {
	C      chan []byte
	filter Filter
	drops  atomic.Int64
}

type Hub struct {
	redis *goredis.Client

	mu   sync.RWMutex
	subs map[*Subscriber]struct{}
	n    atomic.Int64
}

func NewHub(redis *goredis.Client) *Hub {
	return &Hub{redis: redis, subs: map[*Subscriber]struct{}{}}
}

// Start listens on the Redis channel (no-op without Redis).
func (h *Hub) Start(ctx context.Context) {
	if h.redis == nil {
		return
	}
	go func() {
		for ctx.Err() == nil {
			sub := h.redis.Subscribe(ctx, redisChannel)
			ch := sub.Channel(goredis.WithChannelSize(1000))
			for msg := range ch {
				var e Event
				if err := json.Unmarshal([]byte(msg.Payload), &e); err == nil {
					h.broadcast(&e, []byte(msg.Payload))
				}
			}
			sub.Close()
			if ctx.Err() == nil {
				logger.Warn("Realtime Redis subscription dropped, resubscribing")
				time.Sleep(2 * time.Second)
			}
		}
	}()
}

// Publish is installed as part of the manager's Emit hook.
func (h *Hub) Publish(ev whatsapp.OutboundEvent) {
	data, err := json.Marshal(ev.Payload)
	if err != nil {
		return
	}
	e := Event{Event: ev.Event, InstanceID: ev.InstanceID, Timestamp: time.Now().UnixMilli(), Data: data}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	if h.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := h.redis.Publish(ctx, redisChannel, raw).Err()
		cancel()
		if err == nil {
			return // delivered to every replica (including this one) via Start
		}
		logger.Warn("Realtime Redis publish failed, delivering locally", zap.Error(err))
	}
	h.broadcast(&e, raw)
}

func (h *Hub) broadcast(e *Event, raw []byte) {
	if h.n.Load() == 0 {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		if !s.filter.match(e) {
			continue
		}
		select {
		case s.C <- raw:
		default:
			// Slow consumer: drop instead of blocking WhatsApp event processing.
			s.drops.Add(1)
		}
	}
}

func (h *Hub) Subscribe(f Filter) *Subscriber {
	s := &Subscriber{C: make(chan []byte, 256), filter: f}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	h.n.Add(1)
	return s
}

func (h *Hub) Unsubscribe(s *Subscriber) {
	h.mu.Lock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		h.n.Add(-1)
	}
	h.mu.Unlock()
}

// Subscribers returns the number of connected subscribers (metrics).
func (h *Hub) Subscribers() int { return int(h.n.Load()) }

// SplitList parses "a,b , c" query values.
func SplitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
