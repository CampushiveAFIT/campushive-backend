package auth

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/ratelimit"
)

const rateLimitedMessage = "Too many requests. Please try again later."

type LimitOptions struct {
	Limit     int
	WindowSec int
	Key       string
	Scope     ratelimit.Scope
}

type Limiter struct {
	service *ratelimit.Limiter
	logger  *slog.Logger
}

func NewLimiter(service *ratelimit.Limiter, logger *slog.Logger) *Limiter {
	return &Limiter{service: service, logger: logger}
}

func (l *Limiter) Middleware(options LimitOptions) func(http.Handler) http.Handler {
	scope := options.Scope
	if scope == "" {
		scope = ratelimit.ScopeIP
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var key string
			if scope == ratelimit.ScopeUser {
				value := ""
				if principal, ok := identity.PrincipalFrom(r.Context()); ok && principal.Subject != "" {
					value = principal.Subject
				} else {
					value = clientIP(r)
				}
				key = ratelimit.UserKey(options.Key, value)
			} else {
				key = ratelimit.IPKey(options.Key, clientIP(r))
			}
			result := l.service.Hit(r.Context(), key, options.Limit, time.Duration(options.WindowSec)*time.Second)

			header := w.Header()
			header["X-RateLimit-Limit"] = []string{strconv.Itoa(options.Limit)}
			header["X-RateLimit-Remaining"] = []string{strconv.Itoa(result.Remaining)}
			header["X-RateLimit-Reset"] = []string{strconv.FormatInt(int64(math.Ceil(float64(result.ResetAt.UnixMilli())/1000)), 10)}

			if !result.Allowed {
				httpx.WriteError(w, httpx.TooManyRequests(rateLimitedMessage))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	address := httpx.ClientIPFromContext(r.Context())
	if address == "" {
		return "unknown"
	}
	return address
}
