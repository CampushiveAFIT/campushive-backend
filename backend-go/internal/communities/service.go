package communities

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

// normaliseDescription mirrors the reference's `value.trim() ? value.trim() :
// null`: a blank description is stored as NULL rather than an empty string.
func normaliseDescription(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func NewService(db *database.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// activeCommunityOrThrow fetches an active community or returns 404.
func (s *Service) activeCommunityOrThrow(ctx context.Context, id string) (communityRow, error) {
	c, err := scanCommunity(s.db.Pool.QueryRow(ctx,
		`SELECT `+communityColumns+` FROM "Community" WHERE "id" = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return communityRow{}, httpx.NotFoundMessage("Community not found.")
		}
		return communityRow{}, fmt.Errorf("get community: %w", err)
	}
	if !c.IsActive {
		return communityRow{}, httpx.NotFoundMessage("Community not found.")
	}
	return c, nil
}

// memberCount returns the number of ACTIVE members of a community.
func (s *Service) memberCount(ctx context.Context, communityID string) (int, error) {
	var count int
	err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "CommunityMembership" WHERE "communityId" = $1 AND "status" = 'ACTIVE'`,
		communityID).Scan(&count)
	return count, err
}

// toView converts a row to a CommunityView including the live member count.
func (s *Service) toView(ctx context.Context, c communityRow) (CommunityView, error) {
	mc, err := s.memberCount(ctx, c.ID)
	if err != nil {
		return CommunityView{}, err
	}
	return CommunityView{
		ID: c.ID, UniversityID: c.UniversityID, Name: c.Name, Slug: c.Slug,
		Description: c.Description, Type: c.Type, Visibility: c.Visibility,
		OwnerID: c.OwnerID, IsActive: c.IsActive, MemberCount: mc,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}, nil
}

// myMembership returns the caller's membership or nil.
func (s *Service) myMembership(ctx context.Context, userID, communityID string) (*struct {
	ID        string
	Role      string
	Status    string
	CreatedAt time.Time
}, error) {
	var m struct {
		ID        string
		Role      string
		Status    string
		CreatedAt time.Time
	}
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id","role","status","createdAt" FROM "CommunityMembership" WHERE "userId" = $1 AND "communityId" = $2`,
		userID, communityID).Scan(&m.ID, &m.Role, &m.Status, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get membership: %w", err)
	}
	return &m, nil
}

// myActiveUniversityIDs returns the user's ACTIVE university IDs.
func (s *Service) myActiveUniversityIDs(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.db.Pool.Query(ctx,
		`SELECT "universityId" FROM "UniversityMembership" WHERE "userId" = $1 AND "status" = 'ACTIVE'`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("get university memberships: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan university id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// requireCanView enforces visibility: PUBLIC=always, UNIVERSITY=member, PRIVATE=ACTIVE member.
func (s *Service) requireCanView(ctx context.Context, userID string, c communityRow) error {
	switch c.Visibility {
	case "PUBLIC":
		return nil
	case "UNIVERSITY":
		unis, err := s.myActiveUniversityIDs(ctx, userID)
		if err != nil {
			return err
		}
		for _, uid := range unis {
			if uid == c.UniversityID {
				return nil
			}
		}
		return httpx.Forbidden("This community is restricted to university members.")
	default: // PRIVATE
		m, err := s.myMembership(ctx, userID, c.ID)
		if err != nil {
			return err
		}
		if m == nil || m.Status != "ACTIVE" {
			return httpx.Forbidden("This community is private.")
		}
		return nil
	}
}

// ------------------------------------------------------------------ Create

type CreateRequest struct {
	UniversityID string
	Name         string
	Slug         string
	Description  *string
	Type         string // default CLUB
	Visibility   string // default UNIVERSITY
}

func (s *Service) Create(ctx context.Context, userID string, req CreateRequest) (CommunityView, error) {
	// Creator must have an ACTIVE membership in a university that is itself
	// still active — the same gate myUniversity applies in the reference.
	var memberUniversityID string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT um."universityId"
		 FROM "UniversityMembership" um
		 JOIN "University" u ON u."id" = um."universityId"
		 WHERE um."userId" = $1 AND um."status" = 'ACTIVE' AND u."isActive" = true
		 ORDER BY um."updatedAt" DESC
		 LIMIT 1`,
		userID).Scan(&memberUniversityID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CommunityView{}, httpx.Forbidden("Join a university before creating a community.")
		}
		return CommunityView{}, fmt.Errorf("get active university: %w", err)
	}
	if memberUniversityID != req.UniversityID {
		return CommunityView{}, httpx.Forbidden("Communities can only be created in your own university.")
	}

	now := s.now()
	communityID := uuid.NewString()
	membershipID := uuid.NewString()

	ctype := req.Type
	if ctype == "" {
		ctype = "CLUB"
	}
	cvis := req.Visibility
	if cvis == "" {
		cvis = "UNIVERSITY"
	}

	var created communityRow
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			INSERT INTO "Community" ("id","universityId","name","slug","description","type","visibility","ownerId","isActive","createdAt","updatedAt")
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true,$9,$9)
			RETURNING `+communityColumns,
			communityID, req.UniversityID, strings.TrimSpace(req.Name), req.Slug,
			normaliseDescription(req.Description), ctype, cvis, userID, now)
		var err error
		created, err = scanCommunity(row)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO "CommunityMembership" ("id","userId","communityId","role","status","createdAt","updatedAt")
			VALUES ($1,$2,$3,'OWNER','ACTIVE',$4,$4)`,
			membershipID, userID, communityID, now)
		return err
	})
	if err != nil {
		if database.IsUniqueViolation(err) {
			return CommunityView{}, httpx.Conflict(httpx.CodeConflict, "A community with this slug already exists in the university.")
		}
		return CommunityView{}, fmt.Errorf("create community: %w", err)
	}
	return s.toView(ctx, created)
}

// ------------------------------------------------------------------ Detail

func (s *Service) Detail(ctx context.Context, userID, communityID string) (CommunityDetail, error) {
	c, err := s.activeCommunityOrThrow(ctx, communityID)
	if err != nil {
		return CommunityDetail{}, err
	}
	if err := s.requireCanView(ctx, userID, c); err != nil {
		return CommunityDetail{}, err
	}
	view, err := s.toView(ctx, c)
	if err != nil {
		return CommunityDetail{}, err
	}
	m, err := s.myMembership(ctx, userID, communityID)
	if err != nil {
		return CommunityDetail{}, err
	}
	var ms *MembershipShort
	if m != nil {
		ms = &MembershipShort{Role: m.Role, Status: m.Status, CreatedAt: m.CreatedAt}
	}
	return CommunityDetail{Community: view, MyMembership: ms}, nil
}

// ------------------------------------------------------------------ Update

type UpdateRequest struct {
	Name *string
	Slug *string
	// Description is only read when DescriptionSet is true, so a JSON null
	// (clear the column) stays distinct from an absent property.
	Description    *string
	DescriptionSet bool
	Type           *string
	Visibility     *string
}

func (s *Service) Update(ctx context.Context, userID, communityID string, req UpdateRequest) (CommunityView, error) {
	c, err := s.activeCommunityOrThrow(ctx, communityID)
	if err != nil {
		return CommunityView{}, err
	}
	m, err := s.myMembership(ctx, userID, communityID)
	if err != nil {
		return CommunityView{}, err
	}
	if m == nil || m.Status != "ACTIVE" || m.Role != "OWNER" {
		return CommunityView{}, httpx.Forbidden("Only the community owner can update it.")
	}

	sets := []string{}
	args := []any{}
	add := func(col string, val any) {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf(`%q = $%d`, col, len(args)))
	}
	if req.Name != nil {
		add("name", strings.TrimSpace(*req.Name))
	}
	if req.Slug != nil {
		add("slug", *req.Slug)
	}
	if req.DescriptionSet {
		add("description", normaliseDescription(req.Description))
	}
	if req.Type != nil {
		add("type", *req.Type)
	}
	if req.Visibility != nil {
		add("visibility", *req.Visibility)
	}
	if len(sets) == 0 {
		return s.toView(ctx, c)
	}
	now := s.now()
	args = append(args, now)
	sets = append(sets, fmt.Sprintf(`"updatedAt" = $%d`, len(args)))
	args = append(args, communityID)
	query := `UPDATE "Community" SET `
	for i, s := range sets {
		if i > 0 {
			query += ", "
		}
		query += s
	}
	query += fmt.Sprintf(` WHERE "id" = $%d RETURNING `+communityColumns, len(args))

	updated, err := scanCommunity(s.db.Pool.QueryRow(ctx, query, args...))
	if err != nil {
		if database.IsUniqueViolation(err) {
			return CommunityView{}, httpx.Conflict(httpx.CodeConflict, "A community with this slug already exists in the university.")
		}
		return CommunityView{}, fmt.Errorf("update community: %w", err)
	}
	return s.toView(ctx, updated)
}

// ------------------------------------------------------------------ Deactivate

func (s *Service) Deactivate(ctx context.Context, userID, communityID string) (CommunityView, error) {
	if _, err := s.activeCommunityOrThrow(ctx, communityID); err != nil {
		return CommunityView{}, err
	}
	m, err := s.myMembership(ctx, userID, communityID)
	if err != nil {
		return CommunityView{}, err
	}
	if m == nil || m.Status != "ACTIVE" || m.Role != "OWNER" {
		return CommunityView{}, httpx.Forbidden("Only the community owner can deactivate it.")
	}

	now := s.now()
	updated, err := scanCommunity(s.db.Pool.QueryRow(ctx, `
		UPDATE "Community" SET "isActive" = false, "updatedAt" = $1 WHERE "id" = $2 RETURNING `+communityColumns,
		now, communityID))
	if err != nil {
		return CommunityView{}, fmt.Errorf("deactivate community: %w", err)
	}
	return s.toView(ctx, updated)
}

// ------------------------------------------------------------------ Join

func (s *Service) Join(ctx context.Context, userID, communityID string) (MembershipShort, error) {
	c, err := s.activeCommunityOrThrow(ctx, communityID)
	if err != nil {
		return MembershipShort{}, err
	}

	if c.Visibility == "PRIVATE" {
		return MembershipShort{}, httpx.Forbidden("This community is invite-only.")
	}
	if c.Visibility == "UNIVERSITY" {
		unis, err := s.myActiveUniversityIDs(ctx, userID)
		if err != nil {
			return MembershipShort{}, err
		}
		found := false
		for _, uid := range unis {
			if uid == c.UniversityID {
				found = true
				break
			}
		}
		if !found {
			return MembershipShort{}, httpx.Forbidden("This community is restricted to university members.")
		}
	}

	existing, err := s.myMembership(ctx, userID, communityID)
	if err != nil {
		return MembershipShort{}, err
	}
	if existing != nil {
		if existing.Status == "ACTIVE" {
			return MembershipShort{Role: existing.Role, Status: existing.Status, CreatedAt: existing.CreatedAt}, nil
		}
		// SUSPENDED or BANNED — cannot rejoin.
		return MembershipShort{}, httpx.Forbidden("Your membership is not active. Contact a community owner.")
	}

	now := s.now()
	var memberID string
	var createdAt time.Time
	var role, status string
	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO "CommunityMembership" ("id","userId","communityId","role","status","createdAt","updatedAt")
		VALUES ($1,$2,$3,'MEMBER','ACTIVE',$4,$4)
		RETURNING "id","role","status","createdAt"`,
		uuid.NewString(), userID, communityID, now).Scan(&memberID, &role, &status, &createdAt)
	if err != nil {
		if database.IsUniqueViolation(err) {
			// Concurrent join: fetch winner's row.
			m, ferr := s.myMembership(ctx, userID, communityID)
			if ferr != nil {
				return MembershipShort{}, ferr
			}
			if m != nil && m.Status == "ACTIVE" {
				return MembershipShort{Role: m.Role, Status: m.Status, CreatedAt: m.CreatedAt}, nil
			}
		}
		return MembershipShort{}, fmt.Errorf("join community: %w", err)
	}
	return MembershipShort{Role: role, Status: status, CreatedAt: createdAt}, nil
}

// ------------------------------------------------------------------ Leave

func (s *Service) Leave(ctx context.Context, userID, communityID string) (map[string]bool, error) {
	if _, err := s.activeCommunityOrThrow(ctx, communityID); err != nil {
		return nil, err
	}
	m, err := s.myMembership(ctx, userID, communityID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return map[string]bool{"left": false}, nil
	}
	if m.Role == "OWNER" {
		return nil, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError, "Owners cannot leave. Transfer ownership first.")
	}
	if _, err := s.db.Pool.Exec(ctx,
		`DELETE FROM "CommunityMembership" WHERE "id" = $1`, m.ID); err != nil {
		return nil, fmt.Errorf("leave community: %w", err)
	}
	return map[string]bool{"left": true}, nil
}

// ------------------------------------------------------------------ ListMembers

func (s *Service) ListMembers(ctx context.Context, userID, communityID string, page httpx.Page) (httpx.PageResult[MemberListItem], error) {
	c, err := s.activeCommunityOrThrow(ctx, communityID)
	if err != nil {
		return httpx.PageResult[MemberListItem]{}, err
	}
	if err := s.requireCanView(ctx, userID, c); err != nil {
		return httpx.PageResult[MemberListItem]{}, err
	}

	query := `SELECT cm."id", cm."role", cm."status", cm."createdAt",
	                 u."id", u."displayName", u."avatarUrl"
	          FROM "CommunityMembership" cm
	          JOIN "User" u ON u."id" = cm."userId"
	          WHERE cm."communityId" = $1`
	args := []any{communityID}

	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		n := len(args)
		query += fmt.Sprintf(` AND (cm."createdAt" < $%d OR (cm."createdAt" = $%d AND cm."id" < $%d))`, n-1, n-1, n)
	}
	args = append(args, page.Limit+1)
	query += fmt.Sprintf(` ORDER BY cm."createdAt" DESC, cm."id" DESC LIMIT $%d`, len(args))

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[MemberListItem]{}, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	type memberRow struct {
		ID          string
		Role        string
		Status      string
		CreatedAt   time.Time
		UserID      string
		DisplayName *string
		AvatarURL   *string
	}
	var mrows []memberRow
	for rows.Next() {
		var r memberRow
		if err := rows.Scan(&r.ID, &r.Role, &r.Status, &r.CreatedAt,
			&r.UserID, &r.DisplayName, &r.AvatarURL); err != nil {
			return httpx.PageResult[MemberListItem]{}, fmt.Errorf("scan member: %w", err)
		}
		mrows = append(mrows, r)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[MemberListItem]{}, fmt.Errorf("iterate members: %w", err)
	}

	paged := httpx.BuildPage(mrows, page.Limit, func(r memberRow) (time.Time, string) {
		return r.CreatedAt, r.ID
	})
	items := make([]MemberListItem, 0, len(paged.Items))
	for _, r := range paged.Items {
		items = append(items, MemberListItem{
			User:      MemberUser{ID: r.UserID, DisplayName: r.DisplayName, AvatarURL: r.AvatarURL},
			Role:      r.Role,
			Status:    r.Status,
			CreatedAt: r.CreatedAt,
		})
	}
	return httpx.PageResult[MemberListItem]{Items: items, NextCursor: paged.NextCursor}, nil
}

// ------------------------------------------------------------------ UpdateMemberRole

func (s *Service) UpdateMemberRole(ctx context.Context, actorID, communityID, targetUserID, role string) (MembershipShort, error) {
	if _, err := s.activeCommunityOrThrow(ctx, communityID); err != nil {
		return MembershipShort{}, err
	}
	actor, err := s.myMembership(ctx, actorID, communityID)
	if err != nil {
		return MembershipShort{}, err
	}
	if actor == nil || actor.Status != "ACTIVE" || actor.Role != "OWNER" {
		return MembershipShort{}, httpx.Forbidden("Only the community owner can manage roles.")
	}

	target, err := s.myMembership(ctx, targetUserID, communityID)
	if err != nil {
		return MembershipShort{}, err
	}
	if target == nil || target.Status != "ACTIVE" {
		return MembershipShort{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "Active membership not found.")
	}
	if target.Role == "OWNER" {
		return MembershipShort{}, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError, "The owner role cannot be changed here.")
	}

	now := s.now()
	var updatedRole, updatedStatus string
	var updatedCreatedAt time.Time
	if err := s.db.Pool.QueryRow(ctx, `
		UPDATE "CommunityMembership" SET "role" = $1, "updatedAt" = $2
		WHERE "id" = $3
		RETURNING "role","status","createdAt"`,
		role, now, target.ID).Scan(&updatedRole, &updatedStatus, &updatedCreatedAt); err != nil {
		return MembershipShort{}, fmt.Errorf("update member role: %w", err)
	}
	return MembershipShort{Role: updatedRole, Status: updatedStatus, CreatedAt: updatedCreatedAt}, nil
}

// ------------------------------------------------------------------ RemoveMember

func (s *Service) RemoveMember(ctx context.Context, actorID, communityID, targetUserID string) (MembershipShort, error) {
	if _, err := s.activeCommunityOrThrow(ctx, communityID); err != nil {
		return MembershipShort{}, err
	}
	if actorID == targetUserID {
		return MembershipShort{}, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError, "Use leave to exit a community.")
	}
	actor, err := s.myMembership(ctx, actorID, communityID)
	if err != nil {
		return MembershipShort{}, err
	}
	if actor == nil || actor.Status != "ACTIVE" {
		return MembershipShort{}, httpx.Forbidden("Only community managers can remove members.")
	}
	target, err := s.myMembership(ctx, targetUserID, communityID)
	if err != nil {
		return MembershipShort{}, err
	}
	if target == nil || target.Status != "ACTIVE" {
		return MembershipShort{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "Active membership not found.")
	}
	if target.Role == "OWNER" {
		return MembershipShort{}, httpx.Forbidden("The owner cannot be removed.")
	}
	if actor.Role == "MODERATOR" && target.Role != "MEMBER" {
		return MembershipShort{}, httpx.Forbidden("Moderators can only remove ordinary members.")
	}
	if actor.Role != "OWNER" && actor.Role != "MODERATOR" {
		return MembershipShort{}, httpx.Forbidden("Only community managers can remove members.")
	}

	now := s.now()
	var updatedRole, updatedStatus string
	var updatedCreatedAt time.Time
	if err := s.db.Pool.QueryRow(ctx, `
		UPDATE "CommunityMembership" SET "status" = 'BANNED', "updatedAt" = $1
		WHERE "id" = $2
		RETURNING "role","status","createdAt"`,
		now, target.ID).Scan(&updatedRole, &updatedStatus, &updatedCreatedAt); err != nil {
		return MembershipShort{}, fmt.Errorf("ban member: %w", err)
	}
	return MembershipShort{Role: updatedRole, Status: updatedStatus, CreatedAt: updatedCreatedAt}, nil
}
