package messaging

import (
	"context"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// PacingConfig mirrors OpenWA's SEND_PACING_* options.
type PacingConfig struct {
	Enabled bool
	// Daily allowance by session age in days: Schedule[0] on the pairing day,
	// Schedule[1] the next day ... and the last value from then on.
	Schedule []int
	// Max first-contact (cold) conversations per day.
	ColdCap int
	// Consecutive send failures that pause the instance for BreakerPause.
	FailBreaker  int
	BreakerPause time.Duration
}

// Pacer enforces per-instance daily send caps. Counters live in Redis when available
// so they survive restarts; otherwise they are kept in memory.
type Pacer struct {
	cfg   PacingConfig
	redis *goredis.Client

	mu    sync.Mutex
	local map[string]int
	until map[string]time.Time
}

func NewPacer(cfg PacingConfig, redis *goredis.Client) *Pacer {
	if cfg.BreakerPause <= 0 {
		cfg.BreakerPause = 30 * time.Minute
	}
	return &Pacer{cfg: cfg, redis: redis, local: map[string]int{}, until: map[string]time.Time{}}
}

func (p *Pacer) Enabled() bool { return p != nil && p.cfg.Enabled }

// DailyCap returns the allowance for a session paired at pairedAt (nil = brand new).
func (p *Pacer) DailyCap(pairedAt *time.Time, now time.Time) int {
	if len(p.cfg.Schedule) == 0 {
		return 0
	}
	age := 0
	if pairedAt != nil {
		age = int(now.Sub(*pairedAt).Hours() / 24)
	}
	if age >= len(p.cfg.Schedule) {
		age = len(p.cfg.Schedule) - 1
	}
	if age < 0 {
		age = 0
	}
	return p.cfg.Schedule[age]
}

type PacingDecision struct {
	Allowed bool
	Reason  string
	RetryAt time.Time
}

func dayKey(now time.Time) string { return now.UTC().Format("20060102") }

func nextDay(now time.Time) time.Time {
	y, m, d := now.UTC().Date()
	return time.Date(y, m, d+1, 0, 0, 30, 0, time.UTC)
}

// Reserve consumes one unit of today's allowance (and of the cold allowance for a first
// contact). When the cap is reached nothing is consumed and RetryAt tells when to try again.
func (p *Pacer) Reserve(ctx context.Context, instanceID string, pairedAt *time.Time, cold bool) PacingDecision {
	if !p.Enabled() {
		return PacingDecision{Allowed: true}
	}
	now := time.Now()
	if until := p.pausedUntil(ctx, instanceID); until.After(now) {
		return PacingDecision{Reason: fmt.Sprintf("send pacing: paused after %d consecutive failures", p.cfg.FailBreaker), RetryAt: until}
	}
	day := dayKey(now)
	limit := p.DailyCap(pairedAt, now)
	if limit > 0 && !p.incrIfBelow(ctx, "wmb:pace:"+instanceID+":"+day+":total", limit) {
		return PacingDecision{Reason: fmt.Sprintf("send pacing: daily cap of %d messages reached", limit), RetryAt: nextDay(now)}
	}
	if cold && p.cfg.ColdCap > 0 && !p.incrIfBelow(ctx, "wmb:pace:"+instanceID+":"+day+":cold", p.cfg.ColdCap) {
		p.decr(ctx, "wmb:pace:"+instanceID+":"+day+":total")
		return PacingDecision{Reason: fmt.Sprintf("send pacing: daily cap of %d new conversations reached", p.cfg.ColdCap), RetryAt: nextDay(now)}
	}
	return PacingDecision{Allowed: true}
}

// Release gives back a reservation when the send did not happen.
func (p *Pacer) Release(ctx context.Context, instanceID string, cold bool) {
	if !p.Enabled() {
		return
	}
	day := dayKey(time.Now())
	p.decr(ctx, "wmb:pace:"+instanceID+":"+day+":total")
	if cold {
		p.decr(ctx, "wmb:pace:"+instanceID+":"+day+":cold")
	}
}

func (p *Pacer) RecordSuccess(ctx context.Context, instanceID string) {
	if !p.Enabled() {
		return
	}
	key := "wmb:pace:" + instanceID + ":fails"
	if p.redis != nil {
		if p.redis.Del(ctx, key).Err() == nil {
			return
		}
	}
	p.mu.Lock()
	delete(p.local, key)
	p.mu.Unlock()
}

// RecordFailure counts a failed send; reaching the breaker threshold pauses the instance.
func (p *Pacer) RecordFailure(ctx context.Context, instanceID string) {
	if !p.Enabled() || p.cfg.FailBreaker <= 0 {
		return
	}
	key := "wmb:pace:" + instanceID + ":fails"
	n := 0
	if p.redis != nil {
		if v, err := p.redis.Incr(ctx, key).Result(); err == nil {
			p.redis.Expire(ctx, key, time.Hour)
			n = int(v)
		}
	}
	if n == 0 {
		p.mu.Lock()
		p.local[key]++
		n = p.local[key]
		p.mu.Unlock()
	}
	if n >= p.cfg.FailBreaker {
		until := time.Now().Add(p.cfg.BreakerPause)
		if p.redis != nil {
			p.redis.Set(ctx, "wmb:pace:"+instanceID+":paused", until.Unix(), p.cfg.BreakerPause)
		}
		p.mu.Lock()
		p.until[instanceID] = until
		p.mu.Unlock()
		p.RecordSuccess(ctx, instanceID) // reset the counter for the next window
	}
}

func (p *Pacer) pausedUntil(ctx context.Context, instanceID string) time.Time {
	if p.redis != nil {
		if v, err := p.redis.Get(ctx, "wmb:pace:"+instanceID+":paused").Int64(); err == nil {
			return time.Unix(v, 0)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.until[instanceID]
}

// Status reports today's usage for an instance.
func (p *Pacer) Status(ctx context.Context, instanceID string, pairedAt *time.Time) map[string]interface{} {
	now := time.Now()
	day := dayKey(now)
	out := map[string]interface{}{
		"enabled":   p.Enabled(),
		"daily_cap": p.DailyCap(pairedAt, now),
		"cold_cap":  p.cfg.ColdCap,
		"sent":      p.get(ctx, "wmb:pace:"+instanceID+":"+day+":total"),
		"cold_sent": p.get(ctx, "wmb:pace:"+instanceID+":"+day+":cold"),
	}
	if until := p.pausedUntil(ctx, instanceID); until.After(now) {
		out["paused_until"] = until
	}
	return out
}

var incrIfBelowScript = goredis.NewScript(`
local v = tonumber(redis.call('GET', KEYS[1]) or '0')
if v >= tonumber(ARGV[1]) then return 0 end
redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], 172800)
return 1`)

func (p *Pacer) incrIfBelow(ctx context.Context, key string, limit int) bool {
	if p.redis != nil {
		if ok, err := incrIfBelowScript.Run(ctx, p.redis, []string{key}, limit).Int(); err == nil {
			return ok == 1
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.local[key] >= limit {
		return false
	}
	p.local[key]++
	return true
}

func (p *Pacer) decr(ctx context.Context, key string) {
	if p.redis != nil {
		if p.redis.Decr(ctx, key).Err() == nil {
			return
		}
	}
	p.mu.Lock()
	if p.local[key] > 0 {
		p.local[key]--
	}
	p.mu.Unlock()
}

func (p *Pacer) get(ctx context.Context, key string) int {
	if p.redis != nil {
		if v, err := p.redis.Get(ctx, key).Int(); err == nil {
			return v
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.local[key]
}
