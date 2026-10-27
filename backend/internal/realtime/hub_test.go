package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/whatsapp"
)

func recv(t *testing.T, s *Subscriber) *Event {
	t.Helper()
	select {
	case raw := <-s.C:
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		return &e
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

func TestHubFiltersByInstanceAndEvent(t *testing.T) {
	h := NewHub(nil)
	all := h.Subscribe(Filter{})
	onlyA := h.Subscribe(Filter{Instances: map[string]bool{"a": true}})
	onlyConn := h.Subscribe(Filter{Events: []string{"connection"}}) // UAZAPI alias
	defer h.Unsubscribe(all)
	defer h.Unsubscribe(onlyA)
	defer h.Unsubscribe(onlyConn)

	h.Publish(whatsapp.OutboundEvent{InstanceID: "b", Event: "messages", Payload: map[string]interface{}{"x": 1}})
	if e := recv(t, all); e == nil || e.InstanceID != "b" || e.Event != "messages" {
		t.Fatalf("unfiltered subscriber must get everything, got %+v", e)
	}
	if recv(t, onlyA) != nil {
		t.Fatal("instance filter must drop other instances")
	}
	if recv(t, onlyConn) != nil {
		t.Fatal("event filter must drop other events")
	}

	h.Publish(whatsapp.OutboundEvent{InstanceID: "a", Event: "connection.update", Payload: map[string]interface{}{}})
	if recv(t, onlyA) == nil || recv(t, onlyConn) == nil || recv(t, all) == nil {
		t.Fatal("matching subscribers must receive the event")
	}
}

func TestHubDoesNotBlockOnSlowSubscriber(t *testing.T) {
	h := NewHub(nil)
	slow := h.Subscribe(Filter{})
	defer h.Unsubscribe(slow)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.Publish(whatsapp.OutboundEvent{InstanceID: "a", Event: "messages", Payload: map[string]interface{}{}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publishing must never block on a full subscriber")
	}
	if slow.drops.Load() == 0 {
		t.Fatal("overflow must be counted as drops")
	}
}

func TestUnsubscribe(t *testing.T) {
	h := NewHub(nil)
	s := h.Subscribe(Filter{})
	if h.Subscribers() != 1 {
		t.Fatal("one subscriber")
	}
	h.Unsubscribe(s)
	h.Unsubscribe(s) // idempotent
	if h.Subscribers() != 0 {
		t.Fatal("no subscribers left")
	}
}
