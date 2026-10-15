package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var fixedWindowScript = redis.NewScript(`
local current = redis.call('INCR', KEYS[1])
if current == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[2]) end
local ttl = redis.call('PTTL', KEYS[1])
return {current <= tonumber(ARGV[1]) and 1 or 0, math.max(0, tonumber(ARGV[1])-current), ttl}
`)

type Result struct {
	Allowed    bool
	Remaining  int64
	RetryAfter time.Duration
}
type Limiter struct {
	client *redis.Client
	prefix string
}

func New(client *redis.Client) *Limiter { return &Limiter{client: client, prefix: "forgegate:rate"} }
func (l *Limiter) Allow(ctx context.Context, tenantID uuid.UUID, limit int) (Result, error) {
	window := time.Now().UTC().Unix() / 60
	key := fmt.Sprintf("%s:%s:%d", l.prefix, tenantID, window)
	raw, err := fixedWindowScript.Run(ctx, l.client, []string{key}, limit, time.Minute.Milliseconds()).Slice()
	if err != nil {
		return Result{}, fmt.Errorf("redis rate limit: %w", err)
	}
	allowed, _ := raw[0].(int64)
	remaining, _ := raw[1].(int64)
	ttl, _ := raw[2].(int64)
	return Result{Allowed: allowed == 1, Remaining: remaining, RetryAfter: time.Duration(ttl) * time.Millisecond}, nil
}
