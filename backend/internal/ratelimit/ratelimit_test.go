package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLocalLimiter(t *testing.T) {
	l := New(nil)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		res := l.Allow(ctx, "k", 3, time.Minute)
		if !res.Allowed || res.Remaining != 3-i {
			t.Fatalf("hit %d: %+v", i, res)
		}
	}
	if res := l.Allow(ctx, "k", 3, time.Minute); res.Allowed || res.Remaining != 0 {
		t.Fatalf("4th hit must be refused: %+v", res)
	}
	if res := l.Allow(ctx, "other", 3, time.Minute); !res.Allowed {
		t.Fatal("keys must be independent")
	}
	if res := l.Allow(ctx, "k", 0, time.Minute); !res.Allowed {
		t.Fatal("limit 0 disables limiting")
	}
}

func TestLocalLimiterWindowResets(t *testing.T) {
	l := New(nil)
	ctx := context.Background()
	period := 50 * time.Millisecond
	l.Allow(ctx, "k", 1, period)
	if l.Allow(ctx, "k", 1, period).Allowed {
		t.Fatal("second hit in the same window must be refused")
	}
	time.Sleep(period + 10*time.Millisecond)
	if !l.Allow(ctx, "k", 1, period).Allowed {
		t.Fatal("a new window must allow again")
	}
}
