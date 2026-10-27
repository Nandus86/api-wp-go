// Package webhook delivers WhatsApp events to HTTP endpoints reliably.
//
// Flow (transactional outbox):
//
//	manager event -> Emit -> webhook_events_queue (RabbitMQ, durable buffer)
//	  -> ingest: resolve targets, INSERT webhook_deliveries (Postgres = source of truth)
//	  -> webhook_delivery_queue (delivery ids) -> deliver: claim row, POST signed body
//	  -> success: delivered | failure: retrying with exponential backoff | exhausted: failed
//	reconciler: re-enqueues due retries and rows orphaned by a crash; purges old rows.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/rabbitmq"
	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
	"github.com/user/whatsmeow-basileia/pkg/logger"
	"go.uber.org/zap"
)

type Config struct {
	GlobalURL     string
	Secret        string
	Timeout       time.Duration
	MaxAttempts   int
	Workers       int
	RetentionDays int
	UserAgent     string
}

// Observer receives delivery outcomes (used for metrics).
type Observer func(event, outcome string, d time.Duration)

type Service struct {
	store    *Store
	rmq      *rabbitmq.Client
	cfg      Config
	http     *http.Client
	observer Observer
}

func NewService(store *Store, rmq *rabbitmq.Client, cfg Config) *Service {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 8
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 8
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "whatsmeow-basileia-webhook"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = cfg.Workers * 2
	return &Service{
		store: store,
		rmq:   rmq,
		cfg:   cfg,
		http:  &http.Client{Timeout: cfg.Timeout, Transport: transport},
	}
}

func (s *Service) Store() *Store          { return s.store }
func (s *Service) MaxAttempts() int       { return s.cfg.MaxAttempts }
func (s *Service) SetObserver(o Observer) { s.observer = o }
func (s *Service) observe(ev, out string, d time.Duration) {
	if s.observer != nil {
		s.observer(ev, out, d)
	}
}

// Start registers the queue consumers and the background loops.
func (s *Service) Start(ctx context.Context) {
	if err := s.rmq.ConsumeN(rabbitmq.QueueWebhookEvents, 4, s.handleIngest); err != nil {
		logger.Error("Failed to start webhook ingest consumer", zap.Error(err))
	}
	if err := s.rmq.ConsumeN(rabbitmq.QueueWebhookDelivery, s.cfg.Workers, s.handleDelivery); err != nil {
		logger.Error("Failed to start webhook delivery consumer", zap.Error(err))
	}
	go s.reconcileLoop(ctx)
	go s.purgeLoop(ctx)
	logger.Info("Webhook outbox started", zap.Int("workers", s.cfg.Workers), zap.Int("max_attempts", s.cfg.MaxAttempts),
		zap.String("global_url", s.cfg.GlobalURL), zap.Bool("signing", s.cfg.Secret != ""))
}

// Emit is installed as the manager hook: it buffers the event in RabbitMQ.
func (s *Service) Emit(ev whatsapp.OutboundEvent) {
	envelope := map[string]interface{}{
		"instance_id": ev.InstanceID,
		"event":       ev.Event,
		"webhook_url": ev.WebhookURL,
		"payload":     ev.Payload,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		logger.Error("Failed to encode webhook event", zap.Error(err))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.rmq.Publish(ctx, rabbitmq.QueueWebhookEvents, body); err != nil {
		// Broker unavailable: write straight to the outbox so the event is not lost.
		logger.Warn("RabbitMQ unavailable, writing webhook event directly to the outbox", zap.String("event", ev.Event), zap.Error(err))
		go func() {
			if err := s.handleIngest(body); err != nil {
				logger.Error("Webhook event lost: broker and database both unavailable", zap.String("event", ev.Event), zap.Error(err))
			}
		}()
	}
}

type envelope struct {
	InstanceID string          `json:"instance_id"`
	Event      string          `json:"event"`
	WebhookURL string          `json:"webhook_url"`
	Payload    json.RawMessage `json:"payload"`
}

// handleIngest turns one buffered event into outbox rows (one per target).
// Returning an error makes RabbitMQ retry the event later (e.g. database down).
func (s *Service) handleIngest(body []byte) error {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		logger.Error("Dropping malformed webhook envelope", zap.Error(err))
		return nil
	}
	payload := []byte(env.Payload)
	if len(payload) == 0 || string(payload) == "null" {
		// Legacy flat format (pre-0.0.8 messages left in the queue): the whole body was POSTed.
		payload = body
		if env.Event == "" {
			var flat struct {
				Event      string `json:"event"`
				InstanceID string `json:"instance_id"`
			}
			_ = json.Unmarshal(body, &flat)
			env.Event, env.InstanceID = flat.Event, flat.InstanceID
		}
	}
	if env.Event == "" {
		env.Event = "messages"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var deliveries []newDelivery
	seen := map[string]bool{}
	add := func(webhookID, target string) {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			return
		}
		seen[target] = true
		deliveries = append(deliveries, newDelivery{WebhookID: webhookID, InstanceID: env.InstanceID, Event: env.Event, URL: target, Body: string(payload)})
	}

	// Registered webhooks first, so a URL that is both registered and legacy gets its secret/headers.
	if env.InstanceID != "" {
		hooks, err := s.store.targetsFor(ctx, env.InstanceID)
		if err != nil {
			return fmt.Errorf("load webhook targets: %w", err)
		}
		for _, wh := range hooks {
			if Matches(wh.Events, wh.ExcludeMessages, env.Event, payload) {
				add(wh.ID, wh.URL)
			}
		}
	}
	// Legacy target: instance webhook_url, falling back to the global WEBHOOK_URL (unchanged behaviour).
	legacy := env.WebhookURL
	if legacy == "" {
		legacy = s.cfg.GlobalURL
	}
	add("", legacy)

	if len(deliveries) == 0 {
		return nil
	}
	ids, err := s.store.insertDeliveries(ctx, s.cfg.MaxAttempts, deliveries)
	if err != nil {
		return fmt.Errorf("insert webhook deliveries: %w", err)
	}
	for _, id := range ids {
		s.enqueue(id)
	}
	return nil
}

func (s *Service) enqueue(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// If this fails the reconciler picks the row up after its enqueued_at grace period.
	if err := s.rmq.Publish(ctx, rabbitmq.QueueWebhookDelivery, []byte(`{"id":`+strconv.FormatInt(id, 10)+`}`)); err != nil {
		logger.Warn("Failed to enqueue webhook delivery, reconciler will retry", zap.Int64("id", id), zap.Error(err))
	}
}

func (s *Service) handleDelivery(body []byte) error {
	var msg struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &msg); err != nil || msg.ID == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout+15*time.Second)
	defer cancel()

	c, err := s.store.claim(ctx, msg.ID, s.cfg.Timeout+30*time.Second)
	if err != nil {
		return fmt.Errorf("claim delivery %d: %w", msg.ID, err)
	}
	if c == nil {
		return nil // already handled, not due, or owned by another worker
	}

	secret, headers := s.store.secretFor(ctx, c.WebhookID)
	if c.WebhookID == "" {
		secret = s.cfg.Secret
	}
	status, dur, sendErr := s.post(ctx, c, secret, headers)

	if sendErr == nil && status < 400 {
		s.observe(c.Event, "delivered", dur)
		return s.store.markDelivered(ctx, c.ID, status, dur)
	}

	errMsg := ""
	if sendErr != nil {
		errMsg = sendErr.Error()
	} else {
		errMsg = "HTTP " + strconv.Itoa(status)
	}
	if c.Attempts >= c.MaxAttempts {
		s.observe(c.Event, "failed", dur)
		logger.Warn("Webhook delivery failed permanently", zap.Int64("id", c.ID), zap.String("url", c.URL), zap.String("error", errMsg))
		return s.store.markFailed(ctx, c.ID, status, errMsg, dur, nil)
	}
	next := time.Now().Add(Backoff(c.Attempts))
	s.observe(c.Event, "retry", dur)
	logger.Debug("Webhook delivery failed, will retry", zap.Int64("id", c.ID), zap.String("error", errMsg), zap.Time("next", next))
	return s.store.markFailed(ctx, c.ID, status, errMsg, dur, &next)
}

func (s *Service) post(ctx context.Context, c *claimed, secret string, headers map[string]string) (int, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewBufferString(c.Body))
	if err != nil {
		return 0, 0, err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.cfg.UserAgent)
	req.Header.Set("X-Webhook-Event", c.Event)
	req.Header.Set("X-Webhook-Delivery", strconv.FormatInt(c.ID, 10))
	req.Header.Set("X-Webhook-Attempt", strconv.Itoa(c.Attempts))
	req.Header.Set("X-Webhook-Timestamp", ts)
	if c.InstanceID != "" {
		req.Header.Set("X-Webhook-Instance", c.InstanceID)
	}
	if secret != "" {
		req.Header.Set("X-Webhook-Signature", Sign(secret, ts, []byte(c.Body)))
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := s.http.Do(req)
	dur := time.Since(start)
	if err != nil {
		return 0, dur, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return resp.StatusCode, dur, nil
}

// Sign returns "sha256=<hex>" of HMAC-SHA256(secret, timestamp + "." + body).
// Receivers recompute it with the X-Webhook-Timestamp header and reject old timestamps.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

var backoffSteps = []time.Duration{
	10 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute,
	15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour,
}

// Backoff returns the wait before the next attempt after `attempts` failures (with ±10% jitter).
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := backoffSteps[len(backoffSteps)-1]
	if attempts <= len(backoffSteps) {
		d = backoffSteps[attempts-1]
	}
	jitter := time.Duration(rand.Int63n(int64(d)/5+1)) - d/10
	return d + jitter
}

// eventAliases maps UAZAPI event names to the names emitted by this service.
var eventAliases = map[string]string{
	"connection":      "connection.update",
	"messages_update": "receipts.update",
	"receipts":        "receipts.update",
	"call":            "calls.offer",
	"calls":           "calls.offer",
	"presence":        "presence.update",
	"groups":          "groups.update",
	"message":         "messages",
}

func normalizeEvent(e string) string {
	e = strings.TrimSpace(strings.ToLower(e))
	if alias, ok := eventAliases[e]; ok {
		return alias
	}
	return e
}

// Matches reports whether an event passes a webhook's event list and message filters.
func Matches(events, exclude []string, event string, payload []byte) bool {
	if len(events) > 0 {
		ok := false
		for _, e := range events {
			if n := normalizeEvent(e); n == event || n == "*" || n == "all" {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if event != "messages" || len(exclude) == 0 {
		return true
	}
	var p struct {
		Message struct {
			FromMe       bool `json:"fromMe"`
			IsGroup      bool `json:"isGroup"`
			WasSentByApi bool `json:"wasSentByApi"`
		} `json:"message"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return true
	}
	m := p.Message
	for _, ex := range exclude {
		switch strings.ToLower(strings.TrimSpace(ex)) {
		case "fromme", "frommeyes":
			if m.FromMe {
				return false
			}
		case "frommeno":
			if !m.FromMe {
				return false
			}
		case "isgroup", "isgroupyes":
			if m.IsGroup {
				return false
			}
		case "isgroupno":
			if !m.IsGroup {
				return false
			}
		case "wassentbyapi":
			if m.WasSentByApi {
				return false
			}
		case "wasnotsentbyapi":
			if !m.WasSentByApi {
				return false
			}
		}
	}
	return true
}

// ValidateURL accepts absolute http(s) URLs only.
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("invalid webhook url %q: must be an absolute http(s) URL", raw)
	}
	return nil
}

// Test queues a synthetic event to one webhook.
func (s *Service) Test(ctx context.Context, wh *Webhook) (int64, error) {
	instance := ""
	if wh.InstanceID != nil {
		instance = *wh.InstanceID
	}
	body, _ := json.Marshal(map[string]interface{}{
		"event":       "webhook.test",
		"instance_id": instance,
		"data":        map[string]interface{}{"message": "test delivery from whatsmeow-basileia", "timestamp": time.Now().Unix()},
	})
	ids, err := s.store.insertDeliveries(ctx, 1, []newDelivery{{WebhookID: wh.ID, InstanceID: instance, Event: "webhook.test", URL: wh.URL, Body: string(body)}})
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	s.enqueue(ids[0])
	return ids[0], nil
}

// RedriveAndEnqueue resets deliveries and puts them back in the delivery queue.
func (s *Service) RedriveAndEnqueue(ctx context.Context, ids []int64, instanceIDs []string, status string) (int, error) {
	reset, err := s.store.Redrive(ctx, ids, instanceIDs, status, s.cfg.MaxAttempts)
	if err != nil {
		return 0, err
	}
	for _, id := range reset {
		s.enqueue(id)
	}
	return len(reset), nil
}

func (s *Service) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.rmq.IsReady() {
				continue
			}
			qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			ids, err := s.store.dueForEnqueue(qctx, 500)
			cancel()
			if err != nil {
				logger.Warn("Webhook reconciler query failed", zap.Error(err))
				continue
			}
			for _, id := range ids {
				s.enqueue(id)
			}
		}
	}
}

func (s *Service) purgeLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		qctx, cancel := context.WithTimeout(ctx, time.Minute)
		n, err := s.store.Purge(qctx, s.cfg.RetentionDays, 30)
		cancel()
		if err != nil {
			logger.Warn("Webhook purge failed", zap.Error(err))
		} else if n > 0 {
			logger.Info("Purged old webhook deliveries", zap.Int64("rows", n))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
