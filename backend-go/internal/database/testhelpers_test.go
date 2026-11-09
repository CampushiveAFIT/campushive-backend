package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultTestDatabaseURL = "postgresql://astro@localhost:5432/campus_hive_go_test?host=/run/postgresql"

func testDatabaseURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return defaultTestDatabaseURL
}

func skipIfNoDatabase(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(testDatabaseURL())
	if err != nil {
		t.Skipf("invalid TEST_DATABASE_URL: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := Open(ctx, testDatabaseURL())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func cleanTestData(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.Pool.Exec(context.Background(), `TRUNCATE "User", "University" CASCADE`); err != nil {
		t.Fatalf("truncate test data: %v", err)
	}
}
