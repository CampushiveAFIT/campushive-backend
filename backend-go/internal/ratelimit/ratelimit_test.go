package ratelimit_test

import (
	"context"
	"testing"
	"time"

	"campus-hive/backend-go/internal/ratelimit"
	"campus-hive/backend-go/internal/redisx"
	"campus-hive/backend-go/internal/testutil"
	"github.com/redis/go-redis/v9"
)

func newLimiter(t *testing.T) (*ratelimit.Limiter, *redisx.Client) {
	t.Helper()
	redisHandle := testutil.StartRedis(t)
	logger := testutil.TestLogger(t)
	return ratelimit.New(redisHandle.Redis(), logger), redisHandle
}

func TestAllowsUpToTheLimitThenBlocks(t *testing.T) {
	limiter, _ := newLimiter(t)
	ctx := context.Background()
	key := ratelimit.IPKey("auth:login", "203.0.113.7")

	for i := 1; i <= 3; i++ {
		result := limiter.Hit(ctx, key, 3, time.Minute)
		if !result.Allowed {
			t.Fatalf("request %d was blocked", i)
		}
		if result.Remaining != 3-i {
			t.Errorf("request %d remaining = %d, want %d", i, result.Remaining, 3-i)
		}
	}

	blocked := limiter.Hit(ctx, key, 3, time.Minute)
	if blocked.Allowed {
		t.Error("the fourth request should be blocked")
	}
	if blocked.Remaining != 0 {
		t.Errorf("blocked remaining = %d, want 0", blocked.Remaining)
	}
	if !blocked.ResetAt.After(time.Now()) {
		t.Errorf("blocked resetAt = %s, want a future timestamp", blocked.ResetAt)
	}
}

func TestWindowSlidesAndReleasesCapacity(t *testing.T) {
	limiter, _ := newLimiter(t)
	ctx := context.Background()
	key := ratelimit.IPKey("posts:create", "198.51.100.4")

	for i := 0; i < 2; i++ {
		if result := limiter.Hit(ctx, key, 2, time.Minute); !result.Allowed {
			t.Fatalf("request %d was blocked", i+1)
		}
	}
	if result := limiter.Hit(ctx, key, 2, time.Minute); result.Allowed {
		t.Fatal("the third request should be blocked")
	}

	clock := time.Now()
	limiter.SetClock(func() time.Time { return clock.Add(61 * time.Second) })
	defer limiter.SetClock(time.Now)

	result := limiter.Hit(ctx, key, 2, time.Minute)
	if !result.Allowed {
		t.Error("the window should have released its slots")
	}
	if result.Remaining != 1 {
		t.Errorf("remaining = %d, want 1", result.Remaining)
	}
}

func TestBlockedRequestDoesNotConsumeCapacity(t *testing.T) {
	limiter, _ := newLimiter(t)
	ctx := context.Background()
	key := ratelimit.IPKey("comments:create", "192.0.2.10")

	for i := 0; i < 5; i++ {
		limiter.Hit(ctx, key, 2, time.Minute)
	}
	before, err := limiter.Cardinality(ctx, key)
	if err != nil {
		t.Fatalf("cardinality: %v", err)
	}
	if before != 2 {
		t.Errorf("cardinality = %d, want 2 (blocked hits must not be recorded)", before)
	}
}

func TestKeysAreIsolatedPerScopeValueAndRoute(t *testing.T) {
	limiter, _ := newLimiter(t)
	ctx := context.Background()

	first := limiter.Hit(ctx, ratelimit.IPKey("auth:register", "203.0.113.1"), 1, time.Minute)
	second := limiter.Hit(ctx, ratelimit.IPKey("auth:register", "203.0.113.2"), 1, time.Minute)
	if !first.Allowed || !second.Allowed {
		t.Error("different IPs must have independent buckets")
	}
	if result := limiter.Hit(ctx, ratelimit.IPKey("auth:register", "203.0.113.1"), 1, time.Minute); result.Allowed {
		t.Error("the first IP should now be blocked")
	}
	if result := limiter.Hit(ctx, ratelimit.IPKey("auth:login", "203.0.113.1"), 1, time.Minute); !result.Allowed {
		t.Error("a different route must have an independent bucket")
	}
	if result := limiter.Hit(ctx, ratelimit.UserKey("auth:login", "user-1"), 1, time.Minute); !result.Allowed {
		t.Error("a user-scoped bucket must be independent from the IP bucket")
	}
	if result := limiter.Hit(ctx, ratelimit.UserKey("auth:login", "user-1"), 1, time.Minute); result.Allowed {
		t.Error("the user bucket should be exhausted")
	}
}

func TestKeyShapeAndUnknownIPFallback(t *testing.T) {
	if got := ratelimit.IPKey("auth:login", "203.0.113.7"); got != "rl:auth:login:203.0.113.7" {
		t.Errorf("IPKey = %q", got)
	}
	if got := ratelimit.UserKey("posts:create", "user-42"); got != "rl:posts:create:user:user-42" {
		t.Errorf("UserKey = %q", got)
	}
	if got := ratelimit.IPKey("auth:login", ""); got != "rl:auth:login:unknown" {
		t.Errorf("IPKey with an empty address = %q", got)
	}
}

func TestFailOpenWhenRedisIsUnavailable(t *testing.T) {
	logger := testutil.TestLogger(t)
	limiter := ratelimit.New(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: 0}), logger)
	limiter.SetClock(func() time.Time { return time.Unix(1_700_000_000, 0) })

	now := time.Unix(1_700_000_000, 0)
	result := limiter.Hit(context.Background(), ratelimit.IPKey("auth:login", "203.0.113.9"), 5, time.Minute)
	if !result.Allowed {
		t.Error("rate limiting must fail open when redis is unreachable")
	}
	if result.Remaining != 5 {
		t.Errorf("remaining = %d, want the full limit", result.Remaining)
	}
	if !result.ResetAt.Equal(now.Add(time.Minute)) {
		t.Errorf("resetAt = %s, want %s", result.ResetAt, now.Add(time.Minute))
	}
}

func TestSubSecondWindowsAreClampedToOneSecond(t *testing.T) {
	limiter, redisHandle := newLimiter(t)
	ctx := context.Background()
	key := ratelimit.IPKey("ping", "203.0.113.55")

	if result := limiter.Hit(ctx, key, 1, 100*time.Millisecond); !result.Allowed {
		t.Fatal("the first request should be allowed")
	}
	if ttl, err := redisHandle.Redis().TTL(ctx, key).Result(); err != nil {
		t.Fatalf("ttl: %v", err)
	} else if ttl > time.Second {
		t.Errorf("ttl = %s, want at most 1s", ttl)
	}
}

func TestFailsOpenOnUnexpectedRedisPayload(t *testing.T) {
	limiter, redisHandle := newLimiter(t)
	ctx := context.Background()
	key := ratelimit.IPKey("auth:login", "203.0.113.11")
	if err := redisHandle.Redis().Set(ctx, key, "unexpected", time.Minute).Err(); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	result := limiter.Hit(ctx, key, 4, time.Minute)
	if !result.Allowed {
		t.Error("an unexpected payload must fail open")
	}
}
