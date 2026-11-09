package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"campus-hive/backend-go/internal/config"
	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/email"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/users"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	EmailVerifyTTL   = 48 * time.Hour
	PasswordResetTTL = 24 * time.Hour
)

type Service struct {
	db       *database.DB
	hash     *Hasher
	tokens   *TokenService
	email    email.Sender
	logger   *slog.Logger
	frontend string
	now      func() time.Time
}

func NewService(db *database.DB, tokens *TokenService, sender email.Sender, cfg config.Config, logger *slog.Logger) *Service {
	frontendBase := "http://localhost:5173"
	if len(cfg.CORSOrigins) > 0 && cfg.CORSOrigins[0] != "" {
		frontendBase = cfg.CORSOrigins[0]
	}
	return &Service{
		db:       db,
		hash:     NewHasher(),
		tokens:   tokens,
		email:    sender,
		logger:   logger,
		frontend: frontendBase,
		now:      time.Now,
	}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) Hash() *Hasher {
	return s.hash
}

func (s *Service) Tokens() *TokenService {
	return s.tokens
}

type Result struct {
	User         users.SafeUser `json:"user"`
	AccessToken  string         `json:"accessToken"`
	RefreshToken string         `json:"refreshToken"`
}

func (s *Service) Register(ctx context.Context, req RegisterRequest) (users.SafeUser, error) {
	var existingID string
	err := s.db.Pool.QueryRow(ctx, `SELECT "id" FROM "User" WHERE "email" = $1`, req.Email).Scan(&existingID)
	if err == nil {
		return users.SafeUser{}, duplicateEmail()
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return users.SafeUser{}, fmt.Errorf("lookup email: %w", err)
	}

	passwordHash, err := s.hash.Hash(req.Password)
	if err != nil {
		return users.SafeUser{}, fmt.Errorf("hash password: %w", err)
	}
	now := s.now()
	var displayName *string
	if req.DisplayName != nil && *req.DisplayName != "" {
		trimmed := *req.DisplayName
		displayName = &trimmed
	}

	row := s.db.Pool.QueryRow(ctx, `
		INSERT INTO "User" ("id", "email", "passwordHash", "displayName", "role", "verificationStatus", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, 'STUDENT', 'UNVERIFIED', $5, $5)
		RETURNING `+users.UserColumns,
		uuid.NewString(), req.Email, passwordHash, displayName, now)
	user, err := users.ScanUser(row)
	if err != nil {
		if database.IsUniqueViolation(err) && database.ConstraintName(err) == "User_email_key" {
			return users.SafeUser{}, duplicateEmail()
		}
		return users.SafeUser{}, fmt.Errorf("insert user: %w", err)
	}

	s.issueEmailVerification(ctx, user)
	return users.ToSafeUser(user), nil
}

func (s *Service) Login(ctx context.Context, emailAddress, password string) (Result, error) {
	user, err := users.ScanUser(s.db.Pool.QueryRow(ctx,
		`SELECT `+users.UserColumns+` FROM "User" WHERE "email" = $1`, emailAddress))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, invalidCredentials("Invalid credentials.")
		}
		return Result{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return Result{}, invalidCredentials("Invalid credentials.")
	}
	if !s.hash.Verify(user.PasswordHash, password) {
		return Result{}, invalidCredentials("Invalid credentials.")
	}
	return s.issueSession(ctx, user)
}

func (s *Service) Refresh(ctx context.Context, rawRefreshToken string) (Result, error) {
	digest := Digest(rawRefreshToken)
	record, err := s.findRefreshToken(ctx, digest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, revokedToken()
		}
		return Result{}, fmt.Errorf("lookup refresh token: %w", err)
	}

	// Reuse detection: a revoked token that has a successor means the token was
	// replayed, so the whole family is burned.
	if record.RevokedAt != nil {
		if record.ReplacedByTokenID != nil {
			s.revokeFamily(ctx, record.FamilyID)
		}
		return Result{}, revokedToken()
	}
	if !record.ExpiresAt.After(s.now()) {
		return Result{}, expiredToken("Refresh token has expired.")
	}

	user, err := users.ScanUser(s.db.Pool.QueryRow(ctx,
		`SELECT `+users.UserColumns+` FROM "User" WHERE "id" = $1`, record.UserID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, invalidCredentials("Account is no longer active.")
		}
		return Result{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return Result{}, invalidCredentials("Account is no longer active.")
	}

	// Atomic claim: the conditional update serialises concurrent rotations and
	// exactly one request can flip the token from live to revoked.
	rotated, err := s.claimAndRotate(ctx, record)
	if err != nil {
		return Result{}, err
	}
	if !rotated.won {
		return Result{}, s.resolveLostRotation(ctx, digest)
	}

	accessToken, err := s.tokens.SignAccessToken(user.ID, string(user.Role))
	if err != nil {
		return Result{}, fmt.Errorf("sign access token: %w", err)
	}
	return Result{User: users.ToSafeUser(user), AccessToken: accessToken, RefreshToken: rotated.raw}, nil
}

type rotation struct {
	won bool
	raw string
}

func (s *Service) claimAndRotate(ctx context.Context, record refreshTokenRecord) (rotation, error) {
	now := s.now()
	var won bool
	var raw string
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE "RefreshToken" SET "revokedAt" = $1
			 WHERE "tokenHash" = $2 AND "revokedAt" IS NULL AND "expiresAt" > $1`,
			now, record.TokenHash)
		if err != nil {
			return fmt.Errorf("claim refresh token: %w", err)
		}
		if tag.RowsAffected() != 1 {
			won = false
			return nil
		}
		next, err := s.tokens.NewRefreshToken()
		if err != nil {
			return err
		}
		var createdID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO "RefreshToken" ("id", "userId", "tokenHash", "familyId", "expiresAt", "createdAt", "lastUsedAt")
			VALUES ($1, $2, $3, $4, $5, $6, $6)
			RETURNING "id"`,
			uuid.NewString(), record.UserID, next.Digest, record.FamilyID, next.ExpiresAt, now,
		).Scan(&createdID); err != nil {
			return fmt.Errorf("insert successor refresh token: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE "RefreshToken" SET "replacedByTokenId" = $1 WHERE "id" = $2`,
			createdID, record.ID,
		); err != nil {
			return fmt.Errorf("link successor refresh token: %w", err)
		}
		won = true
		raw = next.Raw
		return nil
	})
	if err != nil {
		return rotation{}, err
	}
	return rotation{won: won, raw: raw}, nil
}

// resolveLostRotation classifies the failure after losing the rotation race.
func (s *Service) resolveLostRotation(ctx context.Context, digest string) error {
	current, err := s.findRefreshToken(ctx, digest)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return revokedToken()
		}
		return fmt.Errorf("lookup refresh token: %w", err)
	}
	if current.RevokedAt != nil {
		if current.ReplacedByTokenID != nil {
			s.revokeFamily(ctx, current.FamilyID)
		}
		return revokedToken()
	}
	return expiredToken("Refresh token has expired.")
}

func (s *Service) Logout(ctx context.Context, rawRefreshToken string) (bool, error) {
	tag, err := s.db.Pool.Exec(ctx,
		`UPDATE "RefreshToken" SET "revokedAt" = $1 WHERE "tokenHash" = $2 AND "revokedAt" IS NULL`,
		s.now(), Digest(rawRefreshToken))
	if err != nil {
		return false, fmt.Errorf("revoke refresh token: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (s *Service) ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error {
	user, err := users.ScanUser(s.db.Pool.QueryRow(ctx,
		`SELECT `+users.UserColumns+` FROM "User" WHERE "id" = $1`, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidCredentials("Account is no longer active.")
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return invalidCredentials("Account is no longer active.")
	}
	if !s.hash.Verify(user.PasswordHash, currentPassword) {
		return invalidCredentials("Current password is incorrect.")
	}
	passwordHash, err := s.hash.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE "User" SET "passwordHash" = $1, "updatedAt" = $2 WHERE "id" = $3`,
			passwordHash, s.now(), userID); err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		return revokeAllForUser(ctx, tx, userID, s.now())
	})
}

func (s *Service) RequestPasswordReset(ctx context.Context, emailAddress string) error {
	// Always succeeds so the endpoint cannot be used to enumerate accounts.
	var id string
	var storedEmail string
	var isActive bool
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id", "email", "isActive" FROM "User" WHERE "email" = $1`, emailAddress).
		Scan(&id, &storedEmail, &isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if !isActive {
		return nil
	}
	token, err := s.tokens.NewOneTimeToken()
	if err != nil {
		return err
	}
	if _, err := s.db.Pool.Exec(ctx, `
		INSERT INTO "PasswordResetToken" ("id", "userId", "tokenHash", "expiresAt", "createdAt")
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.NewString(), id, token.Digest, s.now().Add(PasswordResetTTL), s.now()); err != nil {
		return fmt.Errorf("insert password reset token: %w", err)
	}
	return s.sendPasswordReset(ctx, storedEmail, token.Raw)
}

func (s *Service) ConfirmPasswordReset(ctx context.Context, rawToken, newPassword string) error {
	digest := Digest(rawToken)
	var recordID, userID string
	var expiresAt time.Time
	var usedAt *time.Time
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id", "userId", "expiresAt", "usedAt" FROM "PasswordResetToken" WHERE "tokenHash" = $1`, digest).
		Scan(&recordID, &userID, &expiresAt, &usedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidResetToken()
		}
		return fmt.Errorf("lookup password reset token: %w", err)
	}
	if usedAt != nil || !expiresAt.After(s.now()) {
		return invalidResetToken()
	}
	user, err := users.ScanUser(s.db.Pool.QueryRow(ctx,
		`SELECT `+users.UserColumns+` FROM "User" WHERE "id" = $1`, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidResetToken()
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return invalidResetToken()
	}
	passwordHash, err := s.hash.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE "User" SET "passwordHash" = $1, "updatedAt" = $2 WHERE "id" = $3`,
			passwordHash, s.now(), user.ID); err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE "PasswordResetToken" SET "usedAt" = $1 WHERE "id" = $2`,
			s.now(), recordID); err != nil {
			return fmt.Errorf("consume password reset token: %w", err)
		}
		return revokeAllForUser(ctx, tx, user.ID, s.now())
	})
}

func (s *Service) RequestEmailVerification(ctx context.Context, emailAddress string) error {
	var id, storedEmail string
	var isActive bool
	var verifiedAt *time.Time
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id", "email", "isActive", "emailVerifiedAt" FROM "User" WHERE "email" = $1`, emailAddress).
		Scan(&id, &storedEmail, &isActive, &verifiedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if !isActive || verifiedAt != nil {
		return nil
	}
	user := users.User{ID: id, Email: storedEmail}
	s.issueEmailVerification(ctx, user)
	return nil
}

func (s *Service) ConfirmEmailVerification(ctx context.Context, rawToken string) (users.SafeUser, error) {
	digest := Digest(rawToken)
	var recordID, userID string
	var expiresAt time.Time
	var usedAt *time.Time
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id", "userId", "expiresAt", "usedAt" FROM "EmailVerificationToken" WHERE "tokenHash" = $1`, digest).
		Scan(&recordID, &userID, &expiresAt, &usedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return users.SafeUser{}, invalidVerificationToken()
		}
		return users.SafeUser{}, fmt.Errorf("lookup verification token: %w", err)
	}
	if usedAt != nil || !expiresAt.After(s.now()) {
		return users.SafeUser{}, invalidVerificationToken()
	}
	user, err := users.ScanUser(s.db.Pool.QueryRow(ctx,
		`SELECT `+users.UserColumns+` FROM "User" WHERE "id" = $1`, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return users.SafeUser{}, invalidVerificationToken()
		}
		return users.SafeUser{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return users.SafeUser{}, invalidVerificationToken()
	}
	if user.EmailVerifiedAt != nil {
		if _, err := s.db.Pool.Exec(ctx,
			`UPDATE "EmailVerificationToken" SET "usedAt" = $1 WHERE "id" = $2`, s.now(), recordID); err != nil {
			return users.SafeUser{}, fmt.Errorf("consume verification token: %w", err)
		}
		return users.ToSafeUser(user), nil
	}
	now := s.now()
	var updated users.User
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE "User" SET "emailVerifiedAt" = $1, "verificationStatus" = 'VERIFIED', "updatedAt" = $1
			WHERE "id" = $2 RETURNING `+users.UserColumns, now, user.ID)
		updated, err = users.ScanUser(row)
		if err != nil {
			return fmt.Errorf("mark email verified: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE "EmailVerificationToken" SET "usedAt" = $1 WHERE "id" = $2`,
			now, recordID); err != nil {
			return fmt.Errorf("consume verification token: %w", err)
		}
		return nil
	})
	if err != nil {
		return users.SafeUser{}, err
	}
	return users.ToSafeUser(updated), nil
}

func (s *Service) FindActiveUserOrThrow(ctx context.Context, userID string) (users.User, error) {
	user, err := users.ScanUser(s.db.Pool.QueryRow(ctx,
		`SELECT `+users.UserColumns+` FROM "User" WHERE "id" = $1`, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return users.User{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
		}
		return users.User{}, fmt.Errorf("lookup user: %w", err)
	}
	if !user.IsActive {
		return users.User{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
	}
	return user, nil
}

func (s *Service) RevokeAllSessions(ctx context.Context, userID string) error {
	return revokeAllForUser(ctx, s.db.Pool, userID, s.now())
}

type refreshTokenRecord struct {
	ID                string
	UserID            string
	TokenHash         string
	FamilyID          string
	ReplacedByTokenID *string
	ExpiresAt         time.Time
	RevokedAt         *time.Time
}

func (s *Service) findRefreshToken(ctx context.Context, digest string) (refreshTokenRecord, error) {
	var record refreshTokenRecord
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "id", "userId", "tokenHash", "familyId", "replacedByTokenId", "expiresAt", "revokedAt"
		FROM "RefreshToken" WHERE "tokenHash" = $1`, digest).
		Scan(&record.ID, &record.UserID, &record.TokenHash, &record.FamilyID,
			&record.ReplacedByTokenID, &record.ExpiresAt, &record.RevokedAt)
	return record, err
}

func (s *Service) revokeFamily(ctx context.Context, familyID string) {
	if _, err := s.db.Pool.Exec(ctx,
		`UPDATE "RefreshToken" SET "revokedAt" = $1 WHERE "familyId" = $2 AND "revokedAt" IS NULL`,
		s.now(), familyID); err != nil {
		s.logger.Error("revoke refresh token family", "familyId", familyID, "error", err.Error())
	}
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func revokeAllForUser(ctx context.Context, executor execer, userID string, now time.Time) error {
	if _, err := executor.Exec(ctx,
		`UPDATE "RefreshToken" SET "revokedAt" = $1 WHERE "userId" = $2 AND "revokedAt" IS NULL`, now, userID); err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return nil
}

func (s *Service) issueSession(ctx context.Context, user users.User) (Result, error) {
	accessToken, err := s.tokens.SignAccessToken(user.ID, string(user.Role))
	if err != nil {
		return Result{}, fmt.Errorf("sign access token: %w", err)
	}
	next, err := s.tokens.NewRefreshToken()
	if err != nil {
		return Result{}, err
	}
	if _, err := s.db.Pool.Exec(ctx, `
		INSERT INTO "RefreshToken" ("id", "userId", "tokenHash", "familyId", "expiresAt", "createdAt")
		VALUES ($1, $2, $3, $4, $5, $6)`,
		uuid.NewString(), user.ID, next.Digest, uuid.NewString(), next.ExpiresAt, s.now()); err != nil {
		return Result{}, fmt.Errorf("insert refresh token: %w", err)
	}
	return Result{User: users.ToSafeUser(user), AccessToken: accessToken, RefreshToken: next.Raw}, nil
}

func (s *Service) issueEmailVerification(ctx context.Context, user users.User) {
	token, err := s.tokens.NewOneTimeToken()
	if err != nil {
		s.logger.Error("create verification token", "userId", user.ID, "error", err.Error())
		return
	}
	if _, err := s.db.Pool.Exec(ctx, `
		INSERT INTO "EmailVerificationToken" ("id", "userId", "tokenHash", "expiresAt", "createdAt")
		VALUES ($1, $2, $3, $4, $5)`,
		uuid.NewString(), user.ID, token.Digest, s.now().Add(EmailVerifyTTL), s.now()); err != nil {
		s.logger.Error("store verification token", "userId", user.ID, "error", err.Error())
		return
	}
	if err := s.sendVerification(ctx, user.Email, token.Raw); err != nil {
		s.logger.Error("send verification email", "userId", user.ID, "error", err.Error())
	}
}

func (s *Service) sendVerification(ctx context.Context, to, rawToken string) error {
	return s.email.SendVerification(ctx, to, s.frontendBase()+"/verify-email?token="+rawToken)
}

func (s *Service) sendPasswordReset(ctx context.Context, to, rawToken string) error {
	return s.email.SendPasswordReset(ctx, to, s.frontendBase()+"/reset-password?token="+rawToken)
}

func (s *Service) frontendBase() string {
	return s.frontend
}

func duplicateEmail() error {
	return httpx.Conflict(httpx.CodeDuplicateEmail, "An account with this email already exists.")
}

func invalidCredentials(message string) error {
	return httpx.Unauthorized(httpx.CodeInvalidCredentials, message)
}

func revokedToken() error {
	return httpx.Unauthorized(httpx.CodeRevokedToken, "Refresh token is no longer valid.")
}

func expiredToken(message string) error {
	return httpx.Unauthorized(httpx.CodeExpiredToken, message)
}

func invalidResetToken() error {
	return httpx.NewError(http.StatusBadRequest, httpx.CodeExpiredToken, "Password reset token is invalid or has expired.")
}

func invalidVerificationToken() error {
	return httpx.NewError(http.StatusBadRequest, httpx.CodeExpiredToken, "Verification token is invalid or has expired.")
}
