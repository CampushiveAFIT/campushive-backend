package users

import (
	"context"
	"testing"
	"time"

	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/testutil"

	"github.com/google/uuid"
)

func TestUpdateProfileMatchesLegacyColumnSemantics(t *testing.T) {
	testutil.SkipIfNoDatabase(t)
	testutil.ResetDatabase(t)
	db := testutil.OpenDB(t)
	ctx := context.Background()
	service := NewService(db)

	userID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO "User" ("id", "email", "passwordHash", "displayName", "avatarUrl", "bio", "role", "verificationStatus", "createdAt", "updatedAt")
		VALUES ($1, $2, 'hash', 'Ada', 'https://cdn.example.com/a.png', 'hello', 'STUDENT', 'UNVERIFIED', now(), now())`,
		userID, userID+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	initial, err := service.Me(ctx, userID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	empty, err := service.UpdateProfile(ctx, userID, ProfileUpdate{})
	if err != nil {
		t.Fatalf("UpdateProfile(empty): %v", err)
	}
	if !empty.UpdatedAt.Equal(initial.UpdatedAt) {
		t.Errorf("updatedAt changed for an empty update: %v -> %v", initial.UpdatedAt, empty.UpdatedAt)
	}

	if _, err := service.UpdateProfile(ctx, userID, ProfileUpdate{
		DisplayName: UpdateValue{Present: true, Null: true},
	}); err == nil {
		t.Error("expected an error for a null display name")
	} else if status, code, message, _ := httpx.StatusForError(err); status != 500 || code != "INTERNAL_ERROR" ||
		message != "An unexpected error occurred." {
		t.Errorf("error = %d/%s/%q", status, code, message)
	}

	cleared, err := service.UpdateProfile(ctx, userID, ProfileUpdate{
		DisplayName: UpdateValue{Present: true, Text: ""},
		Bio:         UpdateValue{Present: true, Text: ""},
	})
	if err != nil {
		t.Fatalf("UpdateProfile(blank): %v", err)
	}
	if cleared.DisplayName != nil {
		t.Errorf("displayName = %v, want null", *cleared.DisplayName)
	}
	if cleared.Bio == nil || *cleared.Bio != "" {
		t.Errorf("bio = %v, want an empty string", cleared.Bio)
	}
	if cleared.AvatarURL == nil || *cleared.AvatarURL != "https://cdn.example.com/a.png" {
		t.Errorf("avatarUrl = %v, want it untouched", cleared.AvatarURL)
	}
	if !cleared.UpdatedAt.After(initial.UpdatedAt) {
		t.Error("updatedAt was not advanced")
	}

	nulled, err := service.UpdateProfile(ctx, userID, ProfileUpdate{
		Bio:       UpdateValue{Present: true, Null: true},
		AvatarURL: UpdateValue{Present: true, Null: true},
	})
	if err != nil {
		t.Fatalf("UpdateProfile(null): %v", err)
	}
	if nulled.Bio != nil || nulled.AvatarURL != nil {
		t.Errorf("bio/avatarUrl = %v/%v, want null", nulled.Bio, nulled.AvatarURL)
	}

	trimmed, err := service.UpdateProfile(ctx, userID, ProfileUpdate{
		DisplayName: UpdateValue{Present: true, Text: "  Ada  "},
	})
	if err != nil {
		t.Fatalf("UpdateProfile(trim): %v", err)
	}
	if trimmed.DisplayName == nil || *trimmed.DisplayName != "Ada" {
		t.Errorf("displayName = %v, want Ada", trimmed.DisplayName)
	}
	if trimmed.Bio != nil {
		t.Errorf("bio = %v, want it untouched", *trimmed.Bio)
	}
}

func TestMeHidesInactiveUsers(t *testing.T) {
	testutil.SkipIfNoDatabase(t)
	testutil.ResetDatabase(t)
	db := testutil.OpenDB(t)
	ctx := context.Background()
	service := NewService(db)

	userID := uuid.NewString()
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO "User" ("id", "email", "passwordHash", "role", "verificationStatus", "isActive", "createdAt", "updatedAt")
		VALUES ($1, $2, 'hash', 'STUDENT', 'UNVERIFIED', false, now(), now())`, userID, userID+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := service.Me(ctx, userID); err == nil {
		t.Fatal("expected a not found error")
	} else if status, code, message, _ := httpx.StatusForError(err); status != 404 || code != "NOT_FOUND" || message != "User not found." {
		t.Errorf("error = %d/%s/%q", status, code, message)
	}
}
