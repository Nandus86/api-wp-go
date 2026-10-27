package redis

import (
	"context"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// New connects to Redis. addr accepts either a redis:// URL or a plain host:port
// (the docker-compose default is "redis:6379"). It returns nil, nil when addr is empty,
// which callers treat as "Redis disabled".
func New(ctx context.Context, addr, password string, db int) (*goredis.Client, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil
	}
	var opts *goredis.Options
	if strings.HasPrefix(addr, "redis://") || strings.HasPrefix(addr, "rediss://") {
		parsed, err := goredis.ParseURL(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
		}
		opts = parsed
	} else {
		opts = &goredis.Options{Addr: addr, Password: password, DB: db}
	}
	opts.DialTimeout = 5 * time.Second
	opts.ReadTimeout = 3 * time.Second
	opts.WriteTimeout = 3 * time.Second

	client := goredis.NewClient(opts)
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}
	return client, nil
}
