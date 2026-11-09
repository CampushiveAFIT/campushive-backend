package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, databaseURL string) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if poolConfig.MaxConns < 4 {
		poolConfig.MaxConns = 10
	}
	if poolConfig.MaxConnLifetime == 0 {
		poolConfig.MaxConnLifetime = 30 * time.Minute
	}
	if poolConfig.HealthCheckPeriod == 0 {
		poolConfig.HealthCheckPeriod = 30 * time.Second
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() {
	if d != nil && d.Pool != nil {
		d.Pool.Close()
	}
}

func (d *DB) Ping(ctx context.Context) error {
	return d.Pool.Ping(ctx)
}

func (d *DB) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return d.InTxOptions(ctx, pgx.TxOptions{}, fn)
}

func (d *DB) InTxOptions(ctx context.Context, opts pgx.TxOptions, fn func(tx pgx.Tx) error) error {
	tx, err := d.Pool.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func ConstraintName(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

func IsUniqueViolation(err error) bool {
	return ErrorCode(err) == "23505"
}

func IsForeignKeyViolation(err error) bool {
	switch ErrorCode(err) {
	case "23503", "23001":
		return true
	default:
		return false
	}
}

func IsCheckViolation(err error) bool {
	return ErrorCode(err) == "23514"
}
