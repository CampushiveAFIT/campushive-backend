package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed *.sql
var migrationFS embed.FS

const migrationDir = "."

func FS() fs.FS {
	return migrationFS
}

func newMigrator(ctx context.Context, databaseURL string) (*migrate.Migrate, error) {
	source, err := iofs.New(migrationFS, migrationDir)
	if err != nil {
		return nil, fmt.Errorf("open embedded migration source: %w", err)
	}
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open migration database: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	driver, err := migratepg.WithInstance(sqlDB, &migratepg.Config{})
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("open migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("initialise migrator: %w", err)
	}
	return m, nil
}

func Up(ctx context.Context, databaseURL string) (uint, error) {
	m, err := newMigrator(ctx, databaseURL)
	if err != nil {
		return 0, err
	}
	defer closeMigrator(m)
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		version, _, _ := m.Version()
		return version, fmt.Errorf("apply migrations: %w", err)
	}
	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	if dirty {
		return version, fmt.Errorf("migration version %d is dirty; resolve manually", version)
	}
	return version, nil
}

func DownAll(ctx context.Context, databaseURL string) (uint, error) {
	m, err := newMigrator(ctx, databaseURL)
	if err != nil {
		return 0, err
	}
	defer closeMigrator(m)
	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return 0, fmt.Errorf("revert migrations: %w", err)
	}
	return 0, nil
}

func Version(ctx context.Context, databaseURL string) (uint, bool, error) {
	m, err := newMigrator(ctx, databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer closeMigrator(m)
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read migration version: %w", err)
	}
	return version, dirty, nil
}

func Force(ctx context.Context, databaseURL string) (uint, error) {
	m, err := newMigrator(ctx, databaseURL)
	if err != nil {
		return 0, err
	}
	defer closeMigrator(m)
	if err := m.Force(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return 0, fmt.Errorf("clear migration version: %w", err)
	}
	return LatestVersion()
}

func Reset(ctx context.Context, databaseURL string) error {
	version, dirty, err := Version(ctx, databaseURL)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("migration version %d is dirty; resolve manually", version)
	}
	if version > 0 {
		if _, err := DownAll(ctx, databaseURL); err != nil {
			return err
		}
	}
	_, err = Up(ctx, databaseURL)
	return err
}

func LatestVersion() (uint, error) {
	entries, err := migrationFS.ReadDir(migrationDir)
	if err != nil {
		return 0, err
	}
	versions := make([]uint, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		raw, _, _ := strings.Cut(entry.Name(), "_")
		version, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse migration version from %q: %w", entry.Name(), err)
		}
		versions = append(versions, uint(version))
	}
	if len(versions) == 0 {
		return 0, errors.New("no migrations embedded")
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions[len(versions)-1], nil
}

func closeMigrator(m *migrate.Migrate) {
	if m == nil {
		return
	}
	sourceErr, dbErr := m.Close()
	if sourceErr != nil {
		return
	}
	_ = dbErr
}
