// Package ratelimit implements a fixed-window request limiter backed by Redis,
// with an in-process fallback when Redis is not configured or unreachable.
package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type Result struct {
	Allowed   bool
	Limit     int
	Remaining int
	Reset     time.Duration
}

type Limiter struct {
	redis  *goredis.Client
	prefix string

	mu      sync.Mutex
	windows map[string]*window
	lastGC  time.Time
}

type window struct {
	start time.Time
	count int
}

func New(redis *goredis.Client) *Limiter {
	return &Limiter{redis: redis, prefix: "wmb:rl:", windows: map[string]*window{}}
}

// Allow counts one hit for key and reports whether it fits in limit per period.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, period time.Duration) Result {
	if limit <= 0 {
		return Result{Allowed: true, Limit: limit, Remaining: -1}
	}
	if l.redis != nil {
		if res, err := l.allowRedis(ctx, key, limit, period); err == nil {
			return res
		}
		// Redis hiccup: fall through to the local limiter instead of failing open or closed.
	}
	return l.allowLocal(key, limit, period)
}

func (l *Limiter) allowRedis(ctx context.Context, key string, limit int, period time.Duration) (Result, error) {
	now := time.Now()
	bucket := now.Truncate(period)
	rk := fmt.Sprintf("%s%s:%d", l.prefix, key, bucket.Unix())

	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	pipe := l.redis.TxPipeline()
	incr := pipe.Incr(ctx, rk)
	pipe.Expire(ctx, rk, period+time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		return Result{}, err
	}
	n := int(incr.Val())
	return result(n, limit, bucket.Add(period).Sub(now)), nil
}

func (l *Limiter) allowLocal(key string, limit int, period time.Duration) Result {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastGC) > time.Minute {
		for k, w := range l.windows {
			if now.Sub(w.start) > 2*period {
				delete(l.windows, k)
			}
		}
		l.lastGC = now
	}

	bucket := now.Truncate(period)
	w, ok := l.windows[key]
	if !ok || !w.start.Equal(bucket) {
		w = &window{start: bucket}
		l.windows[key] = w
	}
	w.count++
	return result(w.count, limit, bucket.Add(period).Sub(now))
}

func result(count, limit int, reset time.Duration) Result {
	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	return Result{Allowed: count <= limit, Limit: limit, Remaining: remaining, Reset: reset}
}
