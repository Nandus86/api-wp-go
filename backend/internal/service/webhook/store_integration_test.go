package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/database/testdb"
)

func TestOutboxClaimRetryAndRedrive(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	s := NewStore(db)

	ids, err := s.insertDeliveries(ctx, 2, []newDelivery{
		{InstanceID: "i1", Event: "messages", URL: "http://a", Body: `{"n":1}`},
		{InstanceID: "i1", Event: "messages", URL: "http://b", Body: `{"n":2}`},
	})
	if err != nil || len(ids) != 2 {
		t.Fatalf("insert: %v %v", ids, err)
	}

	c, err := s.claim(ctx, ids[0], time.Minute)
	if err != nil || c == nil || c.Attempts != 1 || c.Body != `{"n":1}` {
		t.Fatalf("first claim: %+v %v", c, err)
	}
	if again, _ := s.claim(ctx, ids[0], time.Minute); again != nil {
		t.Fatal("a delivery being processed cannot be claimed twice")
	}

	// Fail with a retry in the past: the reconciler must pick it up.
	past := time.Now().Add(-time.Second)
	if err := s.markFailed(ctx, c.ID, 500, "HTTP 500", 10*time.Millisecond, &past); err != nil {
		t.Fatal(err)
	}
	due, err := s.dueForEnqueue(ctx, 10)
	if err != nil || len(due) != 1 || due[0] != ids[0] {
		t.Fatalf("due rows: %v %v (the other row is already enqueued)", due, err)
	}
	if again, _ := s.dueForEnqueue(ctx, 10); len(again) != 0 {
		t.Fatal("enqueued rows must not be re-enqueued immediately")
	}

	// Second attempt reaches max_attempts and fails for good.
	c, _ = s.claim(ctx, ids[0], time.Minute)
	if c == nil || c.Attempts != 2 {
		t.Fatalf("second claim: %+v", c)
	}
	if err := s.markFailed(ctx, c.ID, 503, "HTTP 503", 0, nil); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.claim(ctx, ids[0], time.Minute); c != nil {
		t.Fatal("failed rows are not claimable")
	}

	// The other one is delivered.
	c2, _ := s.claim(ctx, ids[1], time.Minute)
	if err := s.markDelivered(ctx, c2.ID, 200, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	stats, _ := s.Stats(ctx, nil)
	if stats["failed"] != 1 || stats["delivered"] != 1 {
		t.Fatalf("unexpected stats %v", stats)
	}

	// Redrive resets attempts and makes the row claimable again.
	redriven, err := s.Redrive(ctx, nil, []string{"i1"}, "failed", 3)
	if err != nil || len(redriven) != 1 {
		t.Fatalf("redrive: %v %v", redriven, err)
	}
	c, _ = s.claim(ctx, ids[0], time.Minute)
	if c == nil || c.Attempts != 1 || c.MaxAttempts != 3 {
		t.Fatalf("redriven row must restart from attempt 1: %+v", c)
	}

	// A crashed worker's lease expires and the row comes back.
	if _, err := db.Exec(`UPDATE webhook_deliveries SET locked_until = NOW() - INTERVAL '1 second', enqueued_at = NULL WHERE id = $1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if due, _ := s.dueForEnqueue(ctx, 10); len(due) != 1 {
		t.Fatalf("expired lease must be re-enqueued, got %v", due)
	}
}

func TestWebhookTargets(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	testdb.Exec(t, db, `INSERT INTO instances (id, name) VALUES ('i1', 'one'), ('i2', 'two')`)
	s := NewStore(db)
	i1 := "i1"
	for _, w := range []*Webhook{
		{InstanceID: &i1, URL: "http://own", Enabled: true},
		{InstanceID: nil, URL: "http://global", Enabled: true},
		{InstanceID: &i1, URL: "http://disabled", Enabled: false},
	} {
		if err := s.CreateWebhook(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	targets, err := s.targetsFor(ctx, "i1")
	if err != nil || len(targets) != 2 {
		t.Fatalf("i1 gets its own and the global webhook: %v %v", targets, err)
	}
	if targets, _ := s.targetsFor(ctx, "i2"); len(targets) != 1 || targets[0].URL != "http://global" {
		t.Fatalf("i2 only gets the global webhook: %v", targets)
	}
	// Deleting the instance removes its webhooks (FK cascade).
	testdb.Exec(t, db, `DELETE FROM instances WHERE id = 'i1'`)
	if all, _ := s.ListWebhooks(ctx, "*"); len(all) != 1 {
		t.Fatalf("instance webhooks must be deleted with the instance, got %d", len(all))
	}
}
