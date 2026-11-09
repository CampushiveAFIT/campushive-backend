package httpx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/logging"

	"github.com/google/uuid"
)

type requestContextKey struct{}

type RequestContext struct {
	ID       string
	ClientIP string
	Logger   *slog.Logger
}

func WithRequestContext(ctx context.Context, rc *RequestContext) context.Context {
	return context.WithValue(ctx, requestContextKey{}, rc)
}

// ClientIPFromContext returns the client address resolved by AccessLog, so every
// component derives it from the same trusted-proxy configuration.
func ClientIPFromContext(ctx context.Context) string {
	return RequestContextFrom(ctx).ClientIP
}

func RequestContextFrom(ctx context.Context) *RequestContext {
	if rc, ok := ctx.Value(requestContextKey{}).(*RequestContext); ok && rc != nil {
		return rc
	}
	return &RequestContext{Logger: logging.From(ctx)}
}

func sanitizeRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 128 {
		return ""
	}
	for _, r := range raw {
		isAllowed := r == '-' || r == '_' || r == '.' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !isAllowed {
			return ""
		}
	}
	return raw
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get("X-Request-Id"))
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", id)
		ctx := WithRequestContext(r.Context(), &RequestContext{
			ID:     id,
			Logger: logging.From(r.Context()).With("requestId", id),
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func AccessLog(trustProxy config.TrustProxy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			clientIP := trustProxy.ClientIP(r.RemoteAddr, r.Header.Get("X-Forwarded-For"))
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			rc := RequestContextFrom(r.Context())
			rc.ClientIP = clientIP
			ctx := WithRequestContext(r.Context(), rc)
			next.ServeHTTP(recorder, r.WithContext(ctx))
			logger := RequestContextFrom(r.Context()).Logger
			logger.Info("request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", recorder.status),
				slog.Int("bytes", recorder.bytes),
				slog.Int64("durationMs", time.Since(started).Milliseconds()),
				slog.String("clientIp", clientIP),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger := RequestContextFrom(r.Context()).Logger
				logger.Error("panic recovered",
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				)
				WriteError(w, Internal(nil))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-DNS-Prefetch-Control", "off")
		h.Set("X-Download-Options", "noopen")
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		h.Set("X-XSS-Protection", "0")
		h.Set("Origin-Agent-Cluster", "?1")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self';base-uri 'self';font-src 'self' https: data:;form-action 'self';frame-ancestors 'self';img-src 'self' data:;object-src 'none';script-src 'self';script-src-attr 'none';style-src 'self' https: 'unsafe-inline'")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		h.Del("X-Powered-By")
		next.ServeHTTP(w, r)
	})
}

func CORS(origins []string, maxAge time.Duration) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}
	allowedMethods := "GET,HEAD,PUT,PATCH,POST,DELETE,OPTIONS"
	allowedHeaders := "Authorization,Content-Type,X-Request-Id"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			h := w.Header()
			h.Add("Vary", "Origin")
			if _, ok := allowed[origin]; origin != "" && ok {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Expose-Headers", "X-RateLimit-Limit,X-RateLimit-Remaining,X-RateLimit-Reset,X-Request-Id")
				if r.Method == http.MethodOptions {
					h.Set("Access-Control-Allow-Methods", allowedMethods)
					h.Set("Access-Control-Allow-Headers", allowedHeaders)
					h.Set("Access-Control-Max-Age", formatSeconds(maxAge))
					w.WriteHeader(http.StatusNoContent)
					return
				}
			} else if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func formatSeconds(d time.Duration) string {
	return strconv.FormatInt(int64(d/time.Second), 10)
}

func BodyLimit(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				rejectOversizedBody(w, r, limit)
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

const bodyDrainSlack = 1 << 20

func rejectOversizedBody(w http.ResponseWriter, r *http.Request, limit int64) {
	if r.Body != nil {
		drainable := limit + bodyDrainSlack
		if r.ContentLength > drainable {
			w.Header().Set("Connection", "close")
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, drainable))
		}
	}
	WriteError(w, PayloadTooLarge())
}

func NotFoundHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteError(w, NotFound())
	})
}
