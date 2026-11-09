package ratelimit

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const slidingWindowLua = `
local key = KEYS[1]
local nowMs = tonumber(ARGV[1])
local windowStartMs = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local ttlSec = tonumber(ARGV[4])
local member = ARGV[5]

redis.call('ZREMRANGEBYSCORE', key, '-inf', windowStartMs)
local count = redis.call('ZCARD', key)
if count >= limit then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  local resetAtMs = nowMs + (ttlSec * 1000)
  if #oldest >= 2 then
    resetAtMs = tonumber(oldest[2]) + (ttlSec * 1000)
  end
  return {0, 0, resetAtMs}
end
redis.call('ZADD', key, nowMs, member)
redis.call('EXPIRE', key, ttlSec)
local remaining = limit - count - 1
if remaining < 0 then
  remaining = 0
end
return {1, remaining, nowMs + (ttlSec * 1000)}
`

type Scope string

const (
	ScopeIP   Scope = "ip"
	ScopeUser Scope = "user"
)

type Result struct {
	Allowed   bool
	Remaining int
	ResetAt   time.Time
}

type Limiter struct {
	rdb    redis.UniversalClient
	logger *slog.Logger
	script *redis.Script
	now    func() time.Time
}

func New(rdb redis.UniversalClient, logger *slog.Logger) *Limiter {
	return &Limiter{
		rdb:    rdb,
		logger: logger,
		script: redis.NewScript(slidingWindowLua),
		now:    time.Now,
	}
}

func Key(route string, scope Scope, value string) string {
	return "rl:" + route + ":" + string(scope) + ":" + value
}

// IPKey reproduces the NestJS key layout rl:<key>:<ip>.
func IPKey(route string, ip string) string {
	return "rl:" + route + ":" + normaliseIP(ip)
}

func UserKey(route string, userID string) string {
	return Key(route, ScopeUser, userID)
}

func normaliseIP(ip string) string {
	if ip == "" {
		return "unknown"
	}
	return ip
}

func (l *Limiter) Hit(ctx context.Context, key string, limit int, window time.Duration) Result {
	now := l.now()
	nowMs := now.UnixMilli()
	windowSec := int64(window / time.Second)
	if windowSec < 1 {
		windowSec = 1
	}
	values, err := l.script.Run(ctx, l.rdb,
		[]string{key},
		nowMs,
		nowMs-windowSec*1000,
		limit,
		windowSec,
		fmtMember(nowMs),
	).Int64Slice()
	if err != nil {
		l.logger.Warn("rate-limit redis error, failing open", "key", key, "error", err.Error())
		return Result{Allowed: true, Remaining: limit, ResetAt: now.Add(window)}
	}
	if len(values) < 3 {
		l.logger.Warn("rate-limit redis returned an unexpected payload, failing open", "key", key)
		return Result{Allowed: true, Remaining: limit, ResetAt: now.Add(window)}
	}
	return Result{
		Allowed:   values[0] == 1,
		Remaining: int(values[1]),
		ResetAt:   time.UnixMilli(values[2]),
	}
}

func fmtMember(nowMs int64) string {
	return strconv.FormatInt(nowMs, 10) + ":" + uuid.NewString()
}
