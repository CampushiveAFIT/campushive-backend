package ratelimit

import (
	"context"
	"time"
)

func (l *Limiter) SetClock(now func() time.Time) {
	l.now = now
}

func (l *Limiter) Cardinality(ctx context.Context, key string) (int, error) {
	count, err := l.rdb.ZCard(ctx, key).Result()
	if err != nil {
		return 0, err
	}
	return int(count), nil
}
