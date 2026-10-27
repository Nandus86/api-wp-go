package messaging

import (
	"context"
	"testing"
	"time"

	"github.com/user/whatsmeow-basileia/internal/infrastructure/database/testdb"
)

func job(id, batch string, at time.Time, status string) Job {
	return Job{ID: id, InstanceID: "i1", BatchID: batch, Queue: "send_message_queue", Payload: []byte(`{"number":"1"}`),
		Status: status, MaxAttempts: 3, ScheduledAt: at}
}

func TestJobClaimRetryAndFail(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	s := NewJobStore(db)

	if err := s.insertJobs(ctx, db, []Job{job("j1", "", time.Now(), "queued")}); err != nil {
		t.Fatal(err)
	}
	j, err := s.Claim(ctx, "j1", time.Minute)
	if err != nil || j == nil || j.Attempts != 1 || j.Status != "sending" {
		t.Fatalf("claim: %+v %v", j, err)
	}
	if again, _ := s.Claim(ctx, "j1", time.Minute); again != nil {
		t.Fatal("double claim must not happen")
	}

	retry := time.Now().Add(-time.Second)
	if err := s.MarkFailed(ctx, "j1", "not connected", &retry); err != nil {
		t.Fatal(err)
	}
	due, err := s.DueForEnqueue(ctx, 10)
	if err != nil || len(due) != 1 || due[0].ID != "j1" || due[0].Status != "queued" {
		t.Fatalf("retry must be re-enqueued: %+v %v", due, err)
	}

	j, _ = s.Claim(ctx, "j1", time.Minute)
	if err := s.MarkSent(ctx, j.ID, "WAMSG1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "j1")
	if got.Status != "sent" || got.WAMessageID != "WAMSG1" || got.Attempts != 2 || got.SentAt == nil {
		t.Fatalf("unexpected final job %+v", got)
	}

	// Pacing defers without consuming an attempt.
	_ = s.insertJobs(ctx, db, []Job{job("j2", "", time.Now(), "queued")})
	j2, _ := s.Claim(ctx, "j2", time.Minute)
	if err := s.Defer(ctx, j2.ID, "daily cap", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, "j2"); got.Status != "scheduled" || got.Attempts != 0 {
		t.Fatalf("deferred job must keep its attempts: %+v", got)
	}
	if c, _ := s.Claim(ctx, "j2", time.Minute); c != nil {
		t.Fatal("a job scheduled in the future cannot be claimed yet")
	}
}

func TestBatchPauseResumeAndCancel(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	s := NewJobStore(db)
	start := time.Now().Add(time.Hour)
	b := &Batch{ID: "b1", InstanceID: "i1", Info: "test"}
	jobs := []Job{
		job("b1-1", "b1", start, "scheduled"),
		job("b1-2", "b1", start.Add(30*time.Second), "scheduled"),
	}
	if err := s.CreateBatch(ctx, b, jobs); err != nil {
		t.Fatal(err)
	}

	if ok, err := s.SetBatchState(ctx, "i1", "b1", "stop"); !ok || err != nil {
		t.Fatalf("stop: %v %v", ok, err)
	}
	list, _, _ := s.List(ctx, JobFilter{InstanceID: "i1", BatchID: "b1"})
	for _, j := range list {
		if j.Status != "paused" {
			t.Fatalf("paused batch jobs must be paused, got %s", j.Status)
		}
	}

	if _, err := s.SetBatchState(ctx, "i1", "b1", "continue"); err != nil {
		t.Fatal(err)
	}
	list, _, _ = s.List(ctx, JobFilter{InstanceID: "i1", BatchID: "b1"})
	gap := list[1].ScheduledAt.Sub(list[0].ScheduledAt)
	if list[0].Status != "scheduled" || list[0].ScheduledAt.After(time.Now().Add(5*time.Second)) || gap < 29*time.Second || gap > 31*time.Second {
		t.Fatalf("resume must restart now and keep spacing: %+v / gap %v", list, gap)
	}

	if ok, _ := s.SetBatchState(ctx, "other-instance", "b1", "stop"); ok {
		t.Fatal("a batch of another instance must not be found")
	}

	if n, err := s.CancelPending(ctx, "i1", "b1"); err != nil || n != 2 {
		t.Fatalf("cancel: %d %v", n, err)
	}
	if err := s.RefreshBatches(ctx); err != nil {
		t.Fatal(err)
	}
	batches, _ := s.ListBatches(ctx, "i1", "")
	if len(batches) != 1 || batches[0].Status != "cancelled" || batches[0].Counts["cancelled"] != 2 {
		t.Fatalf("fully cancelled batch: %+v", batches)
	}
}
