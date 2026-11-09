package migrations

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/schemadiff"
)

const defaultTestDatabaseURL = "postgresql://astro@localhost:5432/campus_hive_go_migrations_test?host=/run/postgresql"

func skipIfNoDatabase(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db, err := database.Open(ctx, testDatabaseURL())
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	db.Close()
}

func legacyMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve working directory: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(filepath.Dir(dir), "backend", "prisma", "migrations")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate go.mod")
	return ""
}

func testDatabaseURL() string {
	if url := os.Getenv("TEST_MIGRATIONS_DATABASE_URL"); url != "" {
		return url
	}
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return defaultTestDatabaseURL
}

func cleanReset(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	databaseURL := testDatabaseURL()
	pool := openPool(t, databaseURL)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatalf("drop public schema: %v", err)
	}
	pool.Close()
	if err := Reset(ctx, databaseURL); err != nil {
		t.Fatalf("Reset: %v", err)
	}
}

func TestEmbeddedUpMigrationsMatchLegacyPrismaSQL(t *testing.T) {
	legacyDir := legacyMigrationsDir(t)
	if _, err := os.Stat(legacyDir); err != nil {
		t.Skipf("legacy NestJS migrations are not present at %s", legacyDir)
	}

	entries, err := FS().(interface {
		ReadDir(string) ([]os.DirEntry, error)
	}).ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}

	compared := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		base := strings.TrimSuffix(name, ".up.sql")
		version, title, _ := strings.Cut(base, "_")
		legacyPath := filepath.Join(legacyDir, version+"_"+title, "migration.sql")
		legacy, err := os.ReadFile(legacyPath)
		if err != nil {
			t.Errorf("missing legacy migration for %s: %v", name, err)
			continue
		}
		embedded, err := readEmbedded(name)
		if err != nil {
			t.Errorf("read embedded %s: %v", name, err)
			continue
		}
		if sha256.Sum256(legacy) != sha256.Sum256(embedded) {
			t.Errorf("%s differs from the Prisma migration SQL", name)
		}
		compared++
	}
	if compared == 0 {
		t.Fatal("no migrations were compared")
	}
	t.Logf("compared %d migration files against the Prisma originals", compared)
}

func readEmbedded(name string) ([]byte, error) {
	file, err := FS().Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}

func TestLatestVersionMatchesLegacyTimestamps(t *testing.T) {
	legacyDir := legacyMigrationsDir(t)
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		t.Skipf("legacy NestJS migrations are not present at %s", legacyDir)
	}
	legacyVersions := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			legacyVersions = append(legacyVersions, strings.SplitN(entry.Name(), "_", 2)[0])
		}
	}
	sort.Strings(legacyVersions)
	if len(legacyVersions) == 0 {
		t.Fatal("no legacy migrations found")
	}

	embeddedEntries, err := FS().(interface {
		ReadDir(string) ([]os.DirEntry, error)
	}).ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	embeddedVersions := make([]string, 0, len(embeddedEntries))
	for _, entry := range embeddedEntries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			embeddedVersions = append(embeddedVersions, strings.SplitN(entry.Name(), "_", 2)[0])
		}
	}
	sort.Strings(embeddedVersions)

	if len(embeddedVersions) != len(legacyVersions) {
		t.Fatalf("migration count mismatch: embedded=%d legacy=%d", len(embeddedVersions), len(legacyVersions))
	}
	for i := range legacyVersions {
		if embeddedVersions[i] != legacyVersions[i] {
			t.Errorf("migration %d: embedded=%s legacy=%s", i, embeddedVersions[i], legacyVersions[i])
		}
	}
}

func TestMigrationsApplyToVirginDatabase(t *testing.T) {
	skipIfNoDatabase(t)
	ctx := context.Background()
	databaseURL := testDatabaseURL()

	cleanReset(t)

	latest, err := LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	version, dirty, err := Version(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != latest || dirty {
		t.Fatalf("version=%d dirty=%t latest=%d", version, dirty, latest)
	}

	if _, err := Up(ctx, databaseURL); err != nil {
		t.Fatalf("second Up should be a no-op: %v", err)
	}

	schema, err := schemadiff.Load(ctx, databaseURL)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	for _, table := range []string{
		"User", "RefreshToken", "University", "UniversityMembership", "Community",
		"CommunityMembership", "Follow", "Block", "Post", "Like", "Comment",
		"Event", "Ticket", "DomainEvent", "Notification", "NotificationPreference",
		"Conversation", "ConversationMember", "Message",
	} {
		if !contains(schema.Tables, table) {
			t.Errorf("missing table %q after migration", table)
		}
	}
	for _, table := range []string{"schema_migrations", "_prisma_migrations"} {
		if contains(schema.Tables, table) {
			t.Errorf("%q must not be treated as application schema", table)
		}
	}
	if len(schema.Enums) != 17 {
		t.Errorf("enum count = %d, want 17", len(schema.Enums))
	}

	allSchemaObjects := append(append([]string{}, schema.Constraints...), schema.Indexes...)
	for _, object := range []string{
		"UniversityMembership_universityId_fkey",
		"Post_universityId_fkey",
		"Community_ownerId_fkey",
		"Message_senderId_clientMessageId_key",
		"Conversation_directKey_key",
		"Ticket_eventId_userId_key",
		"Notification_dedupeKey_key",
		"User_email_key",
		"RefreshToken_tokenHash_key",
		"ConversationMember_conversationId_userId_key",
		"Like_userId_postId_key",
		"UniversityMembership_userId_universityId_key",
		"DomainEvent_status_availableAt_idx",
	} {
		found := false
		for _, value := range allSchemaObjects {
			if strings.Contains(value, object) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing constraint or index %q", object)
		}
	}
}

func TestDownMigrationsRevertEverything(t *testing.T) {
	skipIfNoDatabase(t)
	ctx := context.Background()
	databaseURL := testDatabaseURL()

	cleanReset(t)
	before, err := schemadiff.Load(ctx, databaseURL)
	if err != nil {
		t.Fatalf("load catalog before revert: %v", err)
	}
	if _, err := DownAll(ctx, databaseURL); err != nil {
		t.Fatalf("DownAll: %v", err)
	}
	version, _, err := Version(ctx, databaseURL)
	if err != nil {
		t.Fatalf("Version after down: %v", err)
	}
	if version != 0 {
		t.Errorf("version = %d, want 0", version)
	}

	catalog, err := schemadiff.Load(ctx, databaseURL)
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	if len(catalog.Tables) != 0 {
		t.Errorf("tables remain after a full revert: %v", catalog.Tables)
	}
	if len(catalog.Enums) != 0 {
		t.Errorf("enums remain after a full revert: %v", catalog.Enums)
	}

	if _, err := Up(ctx, databaseURL); err != nil {
		t.Fatalf("Up after DownAll: %v", err)
	}
	restored, err := schemadiff.Load(ctx, databaseURL)
	if err != nil {
		t.Fatalf("load catalog after restore: %v", err)
	}
	differences := schemadiff.Compare(restored, before)
	for _, difference := range differences {
		t.Errorf("re-applying migrations changed the schema: %s", difference)
	}
	if len(differences) == 0 {
		t.Logf("re-applying the chain reproduces the identical schema (%s)", restored.Summary())
	}
}

func TestSchemaParityWithPrismaReference(t *testing.T) {
	referenceURL := os.Getenv("SCHEMA_REFERENCE_DATABASE_URL")
	if referenceURL == "" {
		t.Skip("set SCHEMA_REFERENCE_DATABASE_URL to a Prisma-migrated database to verify parity")
	}
	skipIfNoDatabase(t)
	ctx := context.Background()
	databaseURL := testDatabaseURL()

	cleanReset(t)
	candidate, err := schemadiff.Load(ctx, databaseURL)
	if err != nil {
		t.Fatalf("load candidate catalog: %v", err)
	}
	reference, err := schemadiff.Load(ctx, referenceURL)
	if err != nil {
		t.Fatalf("load reference catalog: %v", err)
	}
	differences := schemadiff.Compare(candidate, reference)
	for _, difference := range differences {
		t.Errorf("%s", difference)
	}
	if len(differences) == 0 {
		t.Logf("schema identical to the Prisma reference (%s)", candidate.Summary())
	}
}

func TestUpFailsFastOnDirtyDatabase(t *testing.T) {
	skipIfNoDatabase(t)
	ctx := context.Background()
	databaseURL := testDatabaseURL()
	cleanReset(t)
	pool := openPool(t, databaseURL)
	if _, err := pool.Exec(ctx, `UPDATE "schema_migrations" SET dirty = true`); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}
	if _, err := Up(ctx, databaseURL); err == nil {
		t.Error("Up should refuse to continue on a dirty database")
	}
	if _, err := DownAll(ctx, databaseURL); err == nil {
		t.Error("DownAll should refuse to continue on a dirty database")
	}
}

func TestUpFailsFastOnMissingDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Up(ctx, "postgresql://astro@localhost:59999/nonexistent?host=/run/postgresql&connect_timeout=1"); err == nil {
		t.Error("Up should fail when the database is unreachable")
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
