package graph

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Service struct {
	db  *database.DB
	now func() time.Time
}

func NewService(db *database.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// activeUserOrThrow verifies the target user exists and is active.
func (s *Service) activeUserOrThrow(ctx context.Context, userID string) error {
	var isActive bool
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "isActive" FROM "User" WHERE "id" = $1`, userID).Scan(&isActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if !isActive {
		return httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "User not found.")
	}
	return nil
}

// blockedEitherWay returns true if a block exists in either direction between a and b.
func (s *Service) blockedEitherWay(ctx context.Context, a, b string) (bool, error) {
	if a == b {
		return false, nil
	}
	var count int
	err := s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM "Block"
		WHERE ("blockerId" = $1 AND "blockedId" = $2) OR ("blockerId" = $2 AND "blockedId" = $1)`,
		a, b).Scan(&count)
	return count > 0, err
}

// blockedUserIDs returns all user IDs blocked in either direction with the given user.
func (s *Service) blockedUserIDs(ctx context.Context, userID string) (map[string]bool, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT "blockedId" FROM "Block" WHERE "blockerId" = $1
		UNION
		SELECT "blockerId" FROM "Block" WHERE "blockedId" = $1`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("blocked user IDs: %w", err)
	}
	defer rows.Close()
	blocked := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan blocked ID: %w", err)
		}
		blocked[id] = true
	}
	return blocked, rows.Err()
}

// ------------------------------------------------------------------ Follow

func (s *Service) Follow(ctx context.Context, viewerID, targetID string) (map[string]bool, error) {
	if viewerID == targetID {
		return nil, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError, "You cannot follow yourself.")
	}
	if err := s.activeUserOrThrow(ctx, targetID); err != nil {
		return nil, err
	}
	blocked, err := s.blockedEitherWay(ctx, viewerID, targetID)
	if err != nil {
		return nil, err
	}
	if blocked {
		return nil, httpx.Forbidden("You cannot follow this user.")
	}

	now := s.now()
	followID := uuid.NewString()
	domainEventID := uuid.NewString()

	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO "Follow" ("id","followerId","followingId","createdAt")
			VALUES ($1,$2,$3,$4)`,
			followID, viewerID, targetID, now)
		if err != nil {
			if database.IsUniqueViolation(err) {
				// Already following — idempotent. The failed INSERT aborts the
				// transaction, so signal a rollback and answer outside of it.
				return errDuplicateFollow
			}
			return fmt.Errorf("insert follow: %w", err)
		}
		// Persist outbox event atomically.
		payload := fmt.Sprintf(`{"followerId":%q,"followingId":%q,"followId":%q}`, viewerID, targetID, followID)
		_, err = tx.Exec(ctx, `
			INSERT INTO "DomainEvent" ("id","type","aggregateType","aggregateId","actorId","payload","status","attempts","availableAt","createdAt","updatedAt")
			VALUES ($1,'user.followed','Follow',$2,$3,$4::jsonb,'PENDING',0,$5,$5,$5)`,
			domainEventID, followID, viewerID, payload, now)
		return err
	})
	if err != nil {
		if errors.Is(err, errDuplicateFollow) {
			return map[string]bool{"following": true}, nil
		}
		return nil, err
	}
	return map[string]bool{"following": true}, nil
}

var errDuplicateFollow = errors.New("duplicate follow")

// ------------------------------------------------------------------ Unfollow

func (s *Service) Unfollow(ctx context.Context, viewerID, targetID string) (map[string]bool, error) {
	if _, err := s.db.Pool.Exec(ctx,
		`DELETE FROM "Follow" WHERE "followerId" = $1 AND "followingId" = $2`,
		viewerID, targetID); err != nil {
		return nil, fmt.Errorf("unfollow: %w", err)
	}
	return map[string]bool{"following": false}, nil
}

// excludedFollowers returns the user IDs that must not appear in the viewer's
// follow lists: blocked in either direction. Viewing your own lists applies no
// filter, exactly like the legacy reference.
func (s *Service) excludedFollowers(ctx context.Context, viewerID, targetID string) ([]string, error) {
	if viewerID == targetID {
		return nil, nil
	}
	blocked, err := s.blockedUserIDs(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(blocked))
	for id := range blocked {
		ids = append(ids, id)
	}
	return ids, nil
}

// ------------------------------------------------------------------ Followers

func (s *Service) Followers(ctx context.Context, viewerID, targetID string, page httpx.Page) (httpx.PageResult[FollowEntry], error) {
	if err := s.activeUserOrThrow(ctx, targetID); err != nil {
		return httpx.PageResult[FollowEntry]{}, err
	}
	blocked, err := s.excludedFollowers(ctx, viewerID, targetID)
	if err != nil {
		return httpx.PageResult[FollowEntry]{}, err
	}
	return s.listFollows(ctx, "followers", targetID, blocked, page)
}

// ------------------------------------------------------------------ Following

func (s *Service) Following(ctx context.Context, viewerID, targetID string, page httpx.Page) (httpx.PageResult[FollowEntry], error) {
	if err := s.activeUserOrThrow(ctx, targetID); err != nil {
		return httpx.PageResult[FollowEntry]{}, err
	}
	blocked, err := s.excludedFollowers(ctx, viewerID, targetID)
	if err != nil {
		return httpx.PageResult[FollowEntry]{}, err
	}
	return s.listFollows(ctx, "following", targetID, blocked, page)
}

// listFollows is shared logic for followers and following lists.
func (s *Service) listFollows(ctx context.Context, direction, targetID string, blocked []string, page httpx.Page) (httpx.PageResult[FollowEntry], error) {
	// "followers" lists the users following targetID; "following" lists the
	// users targetID follows. The block filter always applies to the side
	// being listed.
	var filterCol, listedCol string
	if direction == "followers" {
		filterCol = `f."followingId"`
		listedCol = `f."followerId"`
	} else {
		filterCol = `f."followerId"`
		listedCol = `f."followingId"`
	}

	args := []any{targetID}
	conds := []string{filterCol + ` = $1`}
	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		conds = append(conds, fmt.Sprintf(`(f."createdAt" < $%d OR (f."createdAt" = $%d AND f."id" < $%d))`,
			len(args)-1, len(args)-1, len(args)))
	}
	if len(blocked) > 0 {
		placeholders := make([]string, 0, len(blocked))
		for _, id := range blocked {
			args = append(args, id)
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		conds = append(conds, fmt.Sprintf(`%s NOT IN (%s)`, listedCol, strings.Join(placeholders, ",")))
	}
	args = append(args, page.Limit+1)

	query := fmt.Sprintf(`
		SELECT f."id", f."createdAt", u."id", u."displayName", u."avatarUrl"
		FROM "Follow" f
		JOIN "User" u ON u."id" = %s
		WHERE %s
		ORDER BY f."createdAt" DESC, f."id" DESC
		LIMIT $%d`,
		listedCol, strings.Join(conds, " AND "), len(args))

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[FollowEntry]{}, fmt.Errorf("list %s: %w", direction, err)
	}
	defer rows.Close()

	type followRow struct {
		ID          string
		CreatedAt   time.Time
		UserID      string
		DisplayName *string
		AvatarURL   *string
	}
	var frows []followRow
	for rows.Next() {
		var r followRow
		if err := rows.Scan(&r.ID, &r.CreatedAt, &r.UserID, &r.DisplayName, &r.AvatarURL); err != nil {
			return httpx.PageResult[FollowEntry]{}, fmt.Errorf("scan follow: %w", err)
		}
		frows = append(frows, r)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[FollowEntry]{}, fmt.Errorf("iterate follows: %w", err)
	}

	paged := httpx.BuildPage(frows, page.Limit, func(r followRow) (time.Time, string) {
		return r.CreatedAt, r.ID
	})
	items := make([]FollowEntry, 0, len(paged.Items))
	for _, r := range paged.Items {
		items = append(items, FollowEntry{
			User:      GraphUser{ID: r.UserID, DisplayName: r.DisplayName, AvatarURL: r.AvatarURL},
			CreatedAt: r.CreatedAt,
		})
	}
	return httpx.PageResult[FollowEntry]{Items: items, NextCursor: paged.NextCursor}, nil
}

// ------------------------------------------------------------------ Block

func (s *Service) Block(ctx context.Context, viewerID, targetID string) (map[string]bool, error) {
	if viewerID == targetID {
		return nil, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError, "You cannot block yourself.")
	}
	if err := s.activeUserOrThrow(ctx, targetID); err != nil {
		return nil, err
	}

	// Idempotent: already blocked?
	var count int
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "Block" WHERE "blockerId" = $1 AND "blockedId" = $2`,
		viewerID, targetID).Scan(&count); err != nil {
		return nil, fmt.Errorf("check block: %w", err)
	}
	if count > 0 {
		return map[string]bool{"blocked": true}, nil
	}

	// Block + sever follows in both directions atomically.
	now := s.now()
	err := s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO "Block" ("id","blockerId","blockedId","createdAt")
			VALUES ($1,$2,$3,$4)`,
			uuid.NewString(), viewerID, targetID, now); err != nil {
			return fmt.Errorf("insert block: %w", err)
		}
		// Sever follows in both directions.
		if _, err := tx.Exec(ctx, `
			DELETE FROM "Follow"
			WHERE ("followerId" = $1 AND "followingId" = $2)
			   OR ("followerId" = $2 AND "followingId" = $1)`,
			viewerID, targetID); err != nil {
			return fmt.Errorf("delete follows: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]bool{"blocked": true}, nil
}

// ------------------------------------------------------------------ Unblock

func (s *Service) Unblock(ctx context.Context, viewerID, targetID string) (map[string]bool, error) {
	if _, err := s.db.Pool.Exec(ctx,
		`DELETE FROM "Block" WHERE "blockerId" = $1 AND "blockedId" = $2`,
		viewerID, targetID); err != nil {
		return nil, fmt.Errorf("unblock: %w", err)
	}
	return map[string]bool{"blocked": false}, nil
}
