package database

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestOpenAndPing(t *testing.T) {
	skipIfNoDatabase(t)
	db := openTestDB(t)
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if db.Pool == nil {
		t.Fatal("pool is nil")
	}
}

func TestOpenRejectsInvalidURL(t *testing.T) {
	if _, err := Open(context.Background(), "not-a-url"); err == nil {
		t.Fatal("expected an error for an invalid DATABASE_URL")
	}
}

func TestInTxCommits(t *testing.T) {
	skipIfNoDatabase(t)
	db := openTestDB(t)
	ctx := context.Background()

	cleanTestData(t, db)
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO "University" (id, name, slug, "updatedAt") VALUES ($1, $2, $3, now())`,
			"tx-commit-uni", "Commit University", "tx-commit-uni")
		return err
	})
	if err != nil {
		t.Fatalf("InTx: %v", err)
	}
	var name string
	if err := db.Pool.QueryRow(ctx, `SELECT name FROM "University" WHERE id = $1`, "tx-commit-uni").Scan(&name); err != nil {
		t.Fatalf("row was not committed: %v", err)
	}
	if name != "Commit University" {
		t.Errorf("name = %q", name)
	}
}

func TestInTxRollsBack(t *testing.T) {
	skipIfNoDatabase(t)
	db := openTestDB(t)
	ctx := context.Background()

	cleanTestData(t, db)
	sentinel := errTest{}
	err := db.InTx(ctx, func(tx pgx.Tx) error {
		if _, execErr := tx.Exec(ctx, `INSERT INTO "University" (id, name, slug, "updatedAt") VALUES ($1, $2, $3, now())`,
			"tx-rollback-uni", "Rollback University", "tx-rollback-uni"); execErr != nil {
			return execErr
		}
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("InTx error = %v, want sentinel", err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM "University" WHERE id = $1`, "tx-rollback-uni").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Error("transaction was not rolled back")
	}
}

func TestUniqueViolationIsDetected(t *testing.T) {
	skipIfNoDatabase(t)
	db := openTestDB(t)
	ctx := context.Background()

	cleanTestData(t, db)
	insert := func(id, email string) error {
		_, err := db.Pool.Exec(ctx,
			`INSERT INTO "User" (id, email, "passwordHash", "updatedAt") VALUES ($1, $2, $3, now())`,
			id, email, "argon2id-hash")
		return err
	}
	if err := insert("uniq-a", "same@test.local"); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	err := insert("uniq-b", "same@test.local")
	if !IsUniqueViolation(err) {
		t.Fatalf("expected unique violation, got %v", err)
	}
	if name := ConstraintName(err); name != "User_email_key" {
		t.Errorf("constraint = %q, want User_email_key", name)
	}
}

func TestForeignKeyRestrictAndCascade(t *testing.T) {
	skipIfNoDatabase(t)
	db := openTestDB(t)
	ctx := context.Background()

	cleanTestData(t, db)
	_, err := db.Pool.Exec(ctx, `INSERT INTO "University" (id, name, slug, "updatedAt") VALUES ($1,$2,$3,now())`,
		"fk-uni", "FK University", "fk-uni")
	if err != nil {
		t.Fatalf("insert university: %v", err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO "User" (id, email, "passwordHash", "updatedAt") VALUES ($1,$2,$3,now())`,
		"fk-user", "fk@test.local", "argon2id-hash")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	_, err = db.Pool.Exec(ctx, `INSERT INTO "UniversityMembership" (id, "userId", "universityId", "updatedAt") VALUES ($1,$2,$3,now())`,
		"fk-membership", "fk-user", "fk-uni")
	if err != nil {
		t.Fatalf("insert membership: %v", err)
	}

	_, err = db.Pool.Exec(ctx, `DELETE FROM "University" WHERE id = $1`, "fk-uni")
	if !IsForeignKeyViolation(err) {
		t.Fatalf("deleting a referenced university must be RESTRICTed, got %v", err)
	}

	_, err = db.Pool.Exec(ctx, `DELETE FROM "User" WHERE id = $1`, "fk-user")
	if err != nil {
		t.Fatalf("deleting a user must cascade: %v", err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM "UniversityMembership" WHERE id = $1`, "fk-membership").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Error("user deletion did not cascade to memberships")
	}
}

func TestSetNullOnCommunityOwner(t *testing.T) {
	skipIfNoDatabase(t)
	db := openTestDB(t)
	ctx := context.Background()

	cleanTestData(t, db)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO "University" (id, name, slug, "updatedAt") VALUES ($1,$2,$3,now())`,
		"sn-uni", "SN University", "sn-uni"); err != nil {
		t.Fatalf("insert university: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO "User" (id, email, "passwordHash", "updatedAt") VALUES ($1,$2,$3,now())`,
		"sn-user", "sn@test.local", "argon2id-hash"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO "Community" (id, "universityId", name, slug, "ownerId", "updatedAt") VALUES ($1,$2,$3,$4,$5,now())`,
		"sn-community", "sn-uni", "Set Null", "set-null", "sn-user"); err != nil {
		t.Fatalf("insert community: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM "User" WHERE id = $1`, "sn-user"); err != nil {
		t.Fatalf("delete owner: %v", err)
	}
	var ownerID *string
	if err := db.Pool.QueryRow(ctx, `SELECT "ownerId" FROM "Community" WHERE id = $1`, "sn-community").Scan(&ownerID); err != nil {
		t.Fatalf("read owner: %v", err)
	}
	if ownerID != nil && strings.TrimSpace(*ownerID) != "" {
		t.Errorf("ownerId = %q, want NULL", *ownerID)
	}
}

type errTest struct{}

func (errTest) Error() string { return "sentinel" }
