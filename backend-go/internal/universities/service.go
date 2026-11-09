package universities

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CommunityView is a partial view used when listing communities by university.
type CommunityView struct {
	ID           string    `json:"id"`
	UniversityID string    `json:"universityId"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Description  *string   `json:"description"`
	Type         string    `json:"type"`
	Visibility   string    `json:"visibility"`
	OwnerID      *string   `json:"ownerId"`
	IsActive     bool      `json:"isActive"`
	MemberCount  int       `json:"memberCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Service struct {
	db  *database.DB
	now func() time.Time
}

func NewService(db *database.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// List returns active universities newest-first with cursor pagination.
func (s *Service) List(ctx context.Context, page httpx.Page) (httpx.PageResult[UniversityView], error) {
	query := `SELECT ` + universityColumns + ` FROM "University" WHERE "isActive" = true`
	args := []any{}
	if page.Cursor != nil {
		query += ` AND ("createdAt" < $1 OR ("createdAt" = $1 AND "id" < $2))`
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
	}
	query += ` ORDER BY "createdAt" DESC, "id" DESC LIMIT $` + fmt.Sprint(len(args)+1)
	args = append(args, page.Limit+1)

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[UniversityView]{}, fmt.Errorf("list universities: %w", err)
	}
	defer rows.Close()

	var unis []universityRow
	for rows.Next() {
		u, err := scanUniversity(rows)
		if err != nil {
			return httpx.PageResult[UniversityView]{}, fmt.Errorf("scan university: %w", err)
		}
		unis = append(unis, u)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[UniversityView]{}, fmt.Errorf("iterate universities: %w", err)
	}

	items := make([]UniversityView, 0, len(unis))
	for _, u := range unis {
		items = append(items, toView(u))
	}
	return httpx.BuildPage(items, page.Limit, func(u UniversityView) (time.Time, string) {
		return u.CreatedAt, u.ID
	}), nil
}

// GetBySlug returns detail + counts. 404 if missing or inactive.
func (s *Service) GetBySlug(ctx context.Context, slug string) (UniversityDetail, error) {
	u, err := scanUniversity(s.db.Pool.QueryRow(ctx,
		`SELECT `+universityColumns+` FROM "University" WHERE "slug" = $1`, slug))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UniversityDetail{}, httpx.NotFoundMessage("University not found.")
		}
		return UniversityDetail{}, fmt.Errorf("get university: %w", err)
	}
	if !u.IsActive {
		return UniversityDetail{}, httpx.NotFoundMessage("University not found.")
	}

	var memberCount, communityCount int
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "UniversityMembership" WHERE "universityId" = $1 AND "status" = 'ACTIVE'`,
		u.ID).Scan(&memberCount); err != nil {
		return UniversityDetail{}, fmt.Errorf("count members: %w", err)
	}
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "Community" WHERE "universityId" = $1 AND "isActive" = true`,
		u.ID).Scan(&communityCount); err != nil {
		return UniversityDetail{}, fmt.Errorf("count communities: %w", err)
	}
	return UniversityDetail{UniversityView: toView(u), MemberCount: memberCount, CommunityCount: communityCount}, nil
}

// RequireActiveByID validates that a university exists and is active, returning it or 404.
func (s *Service) RequireActiveByID(ctx context.Context, universityID string) (universityRow, error) {
	u, err := scanUniversity(s.db.Pool.QueryRow(ctx,
		`SELECT `+universityColumns+` FROM "University" WHERE "id" = $1`, universityID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return universityRow{}, httpx.NotFoundMessage("University not found.")
		}
		return universityRow{}, fmt.Errorf("get university: %w", err)
	}
	if !u.IsActive {
		return universityRow{}, httpx.NotFoundMessage("University not found.")
	}
	return u, nil
}

// MyUniversity returns the caller's single ACTIVE membership + embedded university.
func (s *Service) MyUniversity(ctx context.Context, userID string) (MembershipView, error) {
	var m struct {
		ID                 string
		Status             string
		Role               string
		VerificationStatus string
		CreatedAt          time.Time
		UpdatedAt          time.Time
	}
	var u universityRow
	err := s.db.Pool.QueryRow(ctx, `
		SELECT um."id", um."status", um."role", um."verificationStatus", um."createdAt", um."updatedAt",
		       u."id", u."name", u."slug", u."domain", u."description", u."logoUrl", u."isActive", u."createdAt", u."updatedAt"
		FROM "UniversityMembership" um
		JOIN "University" u ON u."id" = um."universityId"
		WHERE um."userId" = $1 AND um."status" = 'ACTIVE'
		ORDER BY um."updatedAt" DESC
		LIMIT 1`, userID).Scan(
		&m.ID, &m.Status, &m.Role, &m.VerificationStatus, &m.CreatedAt, &m.UpdatedAt,
		&u.ID, &u.Name, &u.Slug, &u.Domain, &u.Description, &u.LogoURL, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return MembershipView{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "No active university membership.")
		}
		return MembershipView{}, fmt.Errorf("get my university: %w", err)
	}
	if !u.IsActive {
		return MembershipView{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "No active university membership.")
	}
	return MembershipView{
		ID: m.ID, Status: m.Status, Role: m.Role, VerificationStatus: m.VerificationStatus,
		University: toView(u), CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}, nil
}

// JoinOrSwitch idempotently joins a university or switches to it from another.
// Invariant: exactly one ACTIVE membership per user.
func (s *Service) JoinOrSwitch(ctx context.Context, userID, universityID string) (MembershipView, error) {
	if _, err := s.RequireActiveByID(ctx, universityID); err != nil {
		return MembershipView{}, err
	}

	// Check for existing membership in this university.
	var existingID string
	var existingStatus string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id", "status" FROM "UniversityMembership" WHERE "userId" = $1 AND "universityId" = $2`,
		userID, universityID).Scan(&existingID, &existingStatus)
	alreadyExists := err == nil

	if alreadyExists && existingStatus == "ACTIVE" {
		return s.MyUniversity(ctx, userID)
	}

	// Transaction: suspend other active memberships, then create/reactivate this one.
	now := s.now()
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		// Suspend all other ACTIVE memberships.
		if _, err := tx.Exec(ctx, `
			UPDATE "UniversityMembership" SET "status" = 'SUSPENDED', "updatedAt" = $1
			WHERE "userId" = $2 AND "status" = 'ACTIVE' AND "universityId" != $3`,
			now, userID, universityID); err != nil {
			return fmt.Errorf("suspend prior memberships: %w", err)
		}
		if alreadyExists {
			// Reactivate existing.
			if _, err := tx.Exec(ctx, `
				UPDATE "UniversityMembership" SET "status" = 'ACTIVE', "updatedAt" = $1
				WHERE "id" = $2`, now, existingID); err != nil {
				return fmt.Errorf("reactivate membership: %w", err)
			}
		} else {
			// Create new.
			if _, err := tx.Exec(ctx, `
				INSERT INTO "UniversityMembership" ("id","userId","universityId","status","role","verificationStatus","createdAt","updatedAt")
				VALUES ($1,$2,$3,'ACTIVE','STUDENT','UNVERIFIED',$4,$4)`,
				uuid.NewString(), userID, universityID, now); err != nil {
				return fmt.Errorf("create membership: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return MembershipView{}, err
	}
	return s.MyUniversity(ctx, userID)
}

// ListCommunities lists communities of a university visible to the caller.
func (s *Service) ListCommunities(ctx context.Context, slug, viewerID string, page httpx.Page) (httpx.PageResult[CommunityView], error) {
	u, err := s.GetBySlug(ctx, slug)
	if err != nil {
		return httpx.PageResult[CommunityView]{}, err
	}

	// Check if viewer is a member of this university.
	var membershipCount int
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "UniversityMembership" WHERE "userId" = $1 AND "universityId" = $2 AND "status" = 'ACTIVE'`,
		viewerID, u.ID).Scan(&membershipCount); err != nil {
		return httpx.PageResult[CommunityView]{}, fmt.Errorf("check membership: %w", err)
	}
	isMember := membershipCount > 0

	// Build visibility filter.
	var visWhere string
	var visArgs []any
	visArgs = append(visArgs, u.ID)
	if isMember {
		// Can see PUBLIC, UNIVERSITY, and PRIVATE communities they're a member of.
		visArgs = append(visArgs, viewerID)
		visWhere = `("visibility" = 'PUBLIC' OR "visibility" = 'UNIVERSITY' OR EXISTS (
			SELECT 1 FROM "CommunityMembership" cm WHERE cm."communityId" = c."id" AND cm."userId" = $2 AND cm."status" = 'ACTIVE'
		))`
	} else {
		// Non-members can only see PUBLIC.
		visWhere = `"visibility" = 'PUBLIC'`
	}

	base := `FROM "Community" c WHERE c."universityId" = $1 AND c."isActive" = true AND ` + visWhere

	// Cursor.
	cursorWhere := ""
	if page.Cursor != nil {
		n := len(visArgs) + 1
		cursorWhere = fmt.Sprintf(` AND (c."createdAt" < $%d OR (c."createdAt" = $%d AND c."id" < $%d))`,
			n, n, n+1)
		visArgs = append(visArgs, page.Cursor.CreatedAt, page.Cursor.ID)
	}

	limitArg := len(visArgs) + 1
	visArgs = append(visArgs, page.Limit+1)

	rows, err := s.db.Pool.Query(ctx,
		`SELECT c."id", c."universityId", c."name", c."slug", c."description", c."type", c."visibility",
		        c."ownerId", c."isActive", c."createdAt", c."updatedAt"`+
			base+cursorWhere+
			fmt.Sprintf(` ORDER BY c."createdAt" DESC, c."id" DESC LIMIT $%d`, limitArg),
		visArgs...)
	if err != nil {
		return httpx.PageResult[CommunityView]{}, fmt.Errorf("list communities: %w", err)
	}
	defer rows.Close()

	type communityRow struct {
		ID           string
		UniversityID string
		Name         string
		Slug         string
		Description  *string
		Type         string
		Visibility   string
		OwnerID      *string
		IsActive     bool
		CreatedAt    time.Time
		UpdatedAt    time.Time
	}
	var crows []communityRow
	for rows.Next() {
		var c communityRow
		if err := rows.Scan(&c.ID, &c.UniversityID, &c.Name, &c.Slug, &c.Description,
			&c.Type, &c.Visibility, &c.OwnerID, &c.IsActive, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return httpx.PageResult[CommunityView]{}, fmt.Errorf("scan community: %w", err)
		}
		crows = append(crows, c)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[CommunityView]{}, fmt.Errorf("iterate communities: %w", err)
	}

	items := make([]CommunityView, 0, len(crows))
	for _, c := range crows {
		items = append(items, CommunityView{
			ID: c.ID, UniversityID: c.UniversityID, Name: c.Name, Slug: c.Slug,
			Description: c.Description, Type: c.Type, Visibility: c.Visibility,
			OwnerID: c.OwnerID, IsActive: c.IsActive,
			CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		})
	}

	result := httpx.BuildPage(items, page.Limit, func(c CommunityView) (time.Time, string) {
		return c.CreatedAt, c.ID
	})
	for i := range result.Items {
		var memberCount int
		_ = s.db.Pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM "CommunityMembership" WHERE "communityId" = $1 AND "status" = 'ACTIVE'`,
			result.Items[i].ID).Scan(&memberCount)
		result.Items[i].MemberCount = memberCount
	}
	return result, nil
}
