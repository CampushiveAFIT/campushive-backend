package testutil

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/migrations"
	"campus-hive/backend-go/internal/redisx"

	"github.com/jackc/pgx/v5/pgconn"
)

const DefaultTestDatabaseURL = "postgresql://astro@localhost:5432/campus_hive_go_test?host=/run/postgresql"

func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate go.mod from working directory")
	return ""
}

func LegacyBackendDir(t testing.TB) string {
	t.Helper()
	return filepath.Join(filepath.Dir(RepoRoot(t)), "backend")
}

func LegacyMigrationsDir(t testing.TB) string {
	t.Helper()
	return filepath.Join(LegacyBackendDir(t), "prisma", "migrations")
}

func DatabaseURL(t testing.TB) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if base == "" {
		base = DefaultTestDatabaseURL
	}
	if strings.TrimSpace(os.Getenv("TEST_DATABASE_SHARED")) != "" {
		return base
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" {
		return base
	}
	baseName := strings.TrimPrefix(parsed.Path, "/")
	if baseName == "" {
		return base
	}
	return withDatabaseName(base, baseName+"_"+packageDatabaseSuffix(t))
}

func packageDatabaseSuffix(t testing.TB) string {
	t.Helper()
	_, self, _, _ := runtime.Caller(0)
	selfDir := filepath.Dir(self)
	for depth := 1; depth < 16; depth++ {
		_, file, _, ok := runtime.Caller(depth)
		if !ok {
			break
		}
		if filepath.Dir(file) == selfDir {
			continue
		}
		name := strings.ToLower(filepath.Base(filepath.Dir(file)))
		var builder strings.Builder
		for _, r := range name {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
				builder.WriteRune(r)
			default:
				builder.WriteByte('_')
			}
		}
		return builder.String()
	}
	t.Fatalf("could not determine the test package directory")
	return ""
}

func withDatabaseName(databaseURL, name string) string {
	parsed, err := url.Parse(databaseURL)
	if err != nil || parsed.Scheme == "" {
		return databaseURL
	}
	parsed.Path = "/" + name
	return parsed.String()
}

func maintenanceURL(t testing.TB) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if base == "" {
		base = DefaultTestDatabaseURL
	}
	return withDatabaseName(base, "postgres")
}

func ensureDatabase(t testing.TB) {
	t.Helper()
	target := DatabaseURL(t)
	parsed, err := url.Parse(target)
	if err != nil {
		t.Skipf("parse TEST_DATABASE_URL: %v", err)
	}
	name := strings.TrimPrefix(parsed.Path, "/")
	if name == "" || name == "postgres" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := database.Open(ctx, maintenanceURL(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v); set TEST_DATABASE_URL to run database tests", err)
	}
	defer admin.Close()

	var exists bool
	if err := admin.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Skipf("inspect databases: %v", err)
	}
	if exists {
		return
	}
	quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	if _, err := admin.Pool.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42P04" {
			t.Skipf("create database %s: %v", name, err)
		}
	}
}

func SkipIfNoDatabase(t testing.TB) {
	t.Helper()
	ensureDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db, err := database.Open(ctx, DatabaseURL(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v); set TEST_DATABASE_URL to run database tests", err)
	}
	db.Close()
}

func ResetDatabase(t testing.TB) {
	t.Helper()
	ctx := context.Background()
	if err := migrations.Reset(ctx, DatabaseURL(t)); err != nil {
		t.Fatalf("reset test database: %v", err)
	}
}

func OpenDB(t testing.TB) *database.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := database.Open(ctx, DatabaseURL(t))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func StartRedis(t testing.TB) *redisx.Client {
	t.Helper()
	if external := strings.TrimSpace(os.Getenv("TEST_REDIS_URL")); external != "" {
		client, err := redisx.NewForTest(context.Background(), external)
		if err != nil {
			t.Fatalf("connect to TEST_REDIS_URL: %v", err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client
	}

	cfg := config.Config{NodeEnv: "test"}
	if _, err := os.Stat(filepath.Join(RepoRoot(t), "..", "backend", "bin", "redis-server")); err == nil {
		cfg.RedisServerBinary = filepath.Join(RepoRoot(t), "..", "backend", "bin", "redis-server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := redisx.New(ctx, cfg, TestLogger(t))
	if err != nil {
		t.Skipf("redis unavailable (%v)", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Redis().FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush test redis: %v", err)
	}
	return client
}

func TestLogger(t testing.TB) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestJWTConfig() config.JWTCfg {
	return config.JWTCfg{
		AccessSecret:  "go-test-access-secret-not-for-production-use-only-0123456789",
		RefreshSecret: "go-test-refresh-secret-not-for-production-use-only-0123456789",
		AccessTTLSec:  config.DefaultAccessTTLSec,
		RefreshTTLDay: config.DefaultRefreshTTLDay,
	}
}

func TestConfig(t testing.TB) config.Config {
	t.Helper()
	return config.Config{
		NodeEnv:            "test",
		Port:               3999,
		DatabaseURL:        DatabaseURL(t),
		JWT:                TestJWTConfig(),
		CORSOrigins:        []string{"http://localhost:5173"},
		AutoMigrate:        false,
		LogLevel:           "error",
		MaxBodyBytes:       1024 * 1024,
		OutboxPollInterval: 2000,
	}
}
