package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const (
	DefaultPort          = 3000
	DefaultAccessTTLSec  = 900
	DefaultRefreshTTLDay = 30
	DefaultEmailPort     = 587
	DefaultEmailFrom     = "noreply@campushive.com"
	DefaultCORSOrigin    = "http://localhost:5173"
	DefaultTrustProxy    = "loopback"
)

type JWTCfg struct {
	AccessSecret  string
	RefreshSecret string
	AccessTTLSec  int
	RefreshTTLDay int
}

type EmailCfg struct {
	From     string
	Host     string
	Port     int
	User     string
	Password string
}

type Config struct {
	NodeEnv            string
	Port               int
	DatabaseURL        string
	JWT                JWTCfg
	CORSOrigins        []string
	RedisURL           string
	RedisServerBinary  string
	TrustProxy         TrustProxy
	Email              EmailCfg
	AutoMigrate        bool
	LogLevel           string
	MaxBodyBytes       int64
	OutboxPollInterval int
}

var ttlPattern = regexp.MustCompile(`(?i)^\s*(\d+)\s*([smhd])?\s*$`)

func ParseTTLSeconds(raw string, fallback int) int {
	m := ttlPattern.FindStringSubmatch(raw)
	if m == nil {
		return fallback
	}
	value, err := strconv.Atoi(m[1])
	if err != nil {
		return fallback
	}
	unit := "s"
	if m[2] != "" {
		unit = strings.ToLower(m[2])
	}
	switch unit {
	case "m":
		return value * 60
	case "h":
		return value * 3600
	case "d":
		return value * 86400
	default:
		return value
	}
}

func parseIntDefault(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func parseBoolDefault(raw string, fallback bool) bool {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envOr(getenv func(string) string, key, fallback string) string {
	v := strings.TrimSpace(getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func LoadDotEnv() error {
	if _, err := os.Stat(".env"); err != nil {
		return nil
	}
	return godotenv.Load()
}

func Load() (Config, error) {
	_ = LoadDotEnv()
	return LoadFrom(os.Getenv)
}

func LoadDatabaseURL() (string, error) {
	_ = LoadDotEnv()
	url := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if url == "" {
		return "", fmt.Errorf("missing required environment variable: DATABASE_URL")
	}
	return url, nil
}

func LoadFrom(getenv func(string) string) (Config, error) {
	trustProxy, err := ParseTrustProxy(envOr(getenv, "TRUST_PROXY", DefaultTrustProxy))
	if err != nil {
		return Config{}, err
	}
	cors := splitCSV(envOr(getenv, "CORS_ORIGIN", DefaultCORSOrigin))
	cfg := Config{
		NodeEnv:     envOr(getenv, "NODE_ENV", "development"),
		Port:        parseIntDefault(getenv("PORT"), DefaultPort),
		DatabaseURL: strings.TrimSpace(getenv("DATABASE_URL")),
		JWT: JWTCfg{
			AccessSecret:  strings.TrimSpace(getenv("JWT_ACCESS_SECRET")),
			RefreshSecret: strings.TrimSpace(getenv("JWT_REFRESH_SECRET")),
			AccessTTLSec:  ParseTTLSeconds(envOr(getenv, "ACCESS_TOKEN_TTL", "15m"), DefaultAccessTTLSec),
			RefreshTTLDay: parseIntDefault(getenv("REFRESH_TOKEN_TTL_DAYS"), DefaultRefreshTTLDay),
		},
		CORSOrigins:       cors,
		RedisURL:          strings.TrimSpace(getenv("REDIS_URL")),
		RedisServerBinary: strings.TrimSpace(getenv("REDIS_SERVER_BINARY")),
		TrustProxy:        trustProxy,
		Email: EmailCfg{
			From:     envOr(getenv, "EMAIL_FROM", DefaultEmailFrom),
			Host:     strings.TrimSpace(getenv("EMAIL_HOST")),
			Port:     parseIntDefault(getenv("EMAIL_PORT"), DefaultEmailPort),
			User:     strings.TrimSpace(getenv("EMAIL_USER")),
			Password: getenv("EMAIL_PASSWORD"),
		},
		AutoMigrate:        parseBoolDefault(getenv("AUTO_MIGRATE"), false),
		LogLevel:           envOr(getenv, "LOG_LEVEL", "info"),
		MaxBodyBytes:       int64(parseIntDefault(getenv("MAX_BODY_BYTES"), 1024*1024)),
		OutboxPollInterval: parseIntDefault(getenv("OUTBOX_POLL_INTERVAL_MS"), 2000),
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) IsProduction() bool {
	return strings.EqualFold(c.NodeEnv, "production")
}

func (c Config) Validate() error {
	missing := make([]string, 0, 4)
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.JWT.AccessSecret == "" {
		missing = append(missing, "JWT_ACCESS_SECRET")
	}
	if c.JWT.RefreshSecret == "" {
		missing = append(missing, "JWT_REFRESH_SECRET")
	}
	if c.IsProduction() && c.RedisURL == "" {
		missing = append(missing, "REDIS_URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535, got %d", c.Port)
	}
	if c.JWT.AccessTTLSec < 1 {
		return fmt.Errorf("ACCESS_TOKEN_TTL must resolve to a positive number of seconds, got %d", c.JWT.AccessTTLSec)
	}
	if c.JWT.RefreshTTLDay < 1 {
		return fmt.Errorf("REFRESH_TOKEN_TTL_DAYS must be a positive integer, got %d", c.JWT.RefreshTTLDay)
	}
	if len(c.CORSOrigins) == 0 {
		return fmt.Errorf("CORS_ORIGIN must list at least one origin")
	}
	for _, origin := range c.CORSOrigins {
		if origin == "*" {
			return fmt.Errorf("CORS_ORIGIN must not be '*' for credentialed authentication")
		}
	}
	return nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
