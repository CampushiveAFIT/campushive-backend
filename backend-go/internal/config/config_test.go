package config

import (
	"testing"
)

func getenvFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestParseTTLSeconds(t *testing.T) {
	cases := []struct {
		raw      string
		fallback int
		want     int
	}{
		{"15m", 900, 900},
		{"30s", 900, 30},
		{"2h", 900, 7200},
		{"7d", 900, 604800},
		{"900", 900, 900},
		{"  45s ", 900, 45},
		{"12M", 900, 720},
		{"1H", 900, 3600},
		{"", 900, 900},
		{"garbage", 900, 900},
		{"-5m", 900, 900},
		{"1.5h", 900, 900},
	}
	for _, tc := range cases {
		if got := ParseTTLSeconds(tc.raw, tc.fallback); got != tc.want {
			t.Errorf("ParseTTLSeconds(%q, %d) = %d, want %d", tc.raw, tc.fallback, got, tc.want)
		}
	}
}

func baseEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":       "postgresql://astro@localhost:5432/campus_hive_test?host=/run/postgresql",
		"JWT_ACCESS_SECRET":  "access-secret-value",
		"JWT_REFRESH_SECRET": "refresh-secret-value",
	}
}

func TestLoadFromDefaults(t *testing.T) {
	cfg, err := LoadFrom(getenvFrom(baseEnv()))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.NodeEnv != "development" {
		t.Errorf("NodeEnv = %q, want development", cfg.NodeEnv)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.JWT.AccessTTLSec != 900 {
		t.Errorf("AccessTTLSec = %d, want 900", cfg.JWT.AccessTTLSec)
	}
	if cfg.JWT.RefreshTTLDay != 30 {
		t.Errorf("RefreshTTLDay = %d, want 30", cfg.JWT.RefreshTTLDay)
	}
	if len(cfg.CORSOrigins) != 1 || cfg.CORSOrigins[0] != "http://localhost:5173" {
		t.Errorf("CORSOrigins = %v, want [http://localhost:5173]", cfg.CORSOrigins)
	}
	if cfg.Email.From != DefaultEmailFrom {
		t.Errorf("Email.From = %q, want %q", cfg.Email.From, DefaultEmailFrom)
	}
	if cfg.Email.Port != DefaultEmailPort {
		t.Errorf("Email.Port = %d, want %d", cfg.Email.Port, DefaultEmailPort)
	}
	if cfg.MaxBodyBytes != 1024*1024 {
		t.Errorf("MaxBodyBytes = %d, want 1048576", cfg.MaxBodyBytes)
	}
	if cfg.AutoMigrate {
		t.Error("AutoMigrate should default to false")
	}
}

func TestLoadFromOverrides(t *testing.T) {
	env := baseEnv()
	env["NODE_ENV"] = "production"
	env["PORT"] = "8080"
	env["ACCESS_TOKEN_TTL"] = "30m"
	env["REFRESH_TOKEN_TTL_DAYS"] = "7"
	env["CORS_ORIGIN"] = "http://a.test, http://b.test ,"
	env["REDIS_URL"] = "redis://127.0.0.1:6379/0"
	env["AUTO_MIGRATE"] = "true"
	env["LOG_LEVEL"] = "debug"
	env["TRUST_PROXY"] = "2"
	env["EMAIL_FROM"] = "noreply@test.local"

	cfg, err := LoadFrom(getenvFrom(env))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.NodeEnv != "production" || cfg.Port != 8080 {
		t.Errorf("unexpected node env/port: %q %d", cfg.NodeEnv, cfg.Port)
	}
	if cfg.JWT.AccessTTLSec != 1800 || cfg.JWT.RefreshTTLDay != 7 {
		t.Errorf("unexpected jwt ttls: %d %d", cfg.JWT.AccessTTLSec, cfg.JWT.RefreshTTLDay)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[1] != "http://b.test" {
		t.Errorf("unexpected CORSOrigins: %v", cfg.CORSOrigins)
	}
	if !cfg.AutoMigrate || cfg.LogLevel != "debug" {
		t.Errorf("unexpected AutoMigrate/LogLevel: %v %q", cfg.AutoMigrate, cfg.LogLevel)
	}
	if cfg.TrustProxy.hops != 2 {
		t.Errorf("TrustProxy hops = %d, want 2", cfg.TrustProxy.hops)
	}
}

func TestLoadFromRequiresSecrets(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(env map[string]string)
		wantErr bool
	}{
		{"missing database url", func(env map[string]string) { delete(env, "DATABASE_URL") }, true},
		{"missing access secret", func(env map[string]string) { delete(env, "JWT_ACCESS_SECRET") }, true},
		{"missing refresh secret", func(env map[string]string) { delete(env, "JWT_REFRESH_SECRET") }, true},
		{"missing redis in production", func(env map[string]string) { env["NODE_ENV"] = "production" }, true},
		{"wildcard cors", func(env map[string]string) { env["CORS_ORIGIN"] = "*" }, true},
		{"empty cors", func(env map[string]string) { env["CORS_ORIGIN"] = " , " }, true},
		{"bad port", func(env map[string]string) { env["PORT"] = "70000" }, true},
		{"bad trust proxy", func(env map[string]string) { env["TRUST_PROXY"] = "maybe" }, true},
		{"production with redis", func(env map[string]string) {
			env["NODE_ENV"] = "production"
			env["REDIS_URL"] = "redis://127.0.0.1:6379/0"
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			tc.mutate(env)
			_, err := LoadFrom(getenvFrom(env))
			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSecretsNeverPrinted(t *testing.T) {
	env := baseEnv()
	env["JWT_ACCESS_SECRET"] = "super-secret-access"
	env["JWT_REFRESH_SECRET"] = "super-secret-refresh"
	cfg, err := LoadFrom(getenvFrom(env))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.JWT.AccessSecret == "" || cfg.JWT.RefreshSecret == "" {
		t.Fatal("secrets were not loaded")
	}
	if cfg.Email.Password != "" {
		t.Error("email password should be empty when unset")
	}
}
