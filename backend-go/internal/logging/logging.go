package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

type ctxKey struct{}

func New(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler)
}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

func From(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

var redactedAttrs = []string{
	"password",
	"newpassword",
	"currentpassword",
	"refreshtoken",
	"accesstoken",
	"token",
	"tokenhash",
	"authorization",
	"body",
	"secret",
}

func Redact(key string, value any) slog.Attr {
	lowered := strings.ToLower(key)
	for _, candidate := range redactedAttrs {
		if lowered == candidate || strings.Contains(lowered, candidate) {
			return slog.String(key, "[redacted]")
		}
	}
	return slog.Any(key, value)
}
