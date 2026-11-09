package posts

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

// ------------------------------------------------------------------ helpers

func (s *Service) activeUserOrThrow(ctx context.Context, userID string) error {
	var isActive bool
	err := s.db.Pool.QueryRow(ctx, `SELECT "isActive" FROM "User" WHERE "id" = $1`, userID).Scan(&isActive)
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

func (s *Service) myActiveMembership(ctx context.Context, userID string) (struct {
	UniversityID     string
	UniversityActive bool
}, error) {
	var m struct {
		UniversityID     string
		UniversityActive bool
	}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT um."universityId", u."isActive"
		FROM "UniversityMembership" um
		JOIN "University" u ON u."id" = um."universityId"
		WHERE um."userId" = $1 AND um."status" = 'ACTIVE'
		ORDER BY um."updatedAt" DESC
		LIMIT 1`, userID).Scan(&m.UniversityID, &m.UniversityActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return m, nil // empty = no membership
		}
		return m, fmt.Errorf("get active membership: %w", err)
	}
	return m, nil
}

// gate enforces the post visibility gate: 404 if missing, deleted, or invisible.
func (s *Service) gate(ctx context.Context, viewerID, postID string) (postRow, ViewerContext, error) {
	row := s.db.Pool.QueryRow(ctx, `
		SELECT `+postColumns+`
		`+postJoins+`
		WHERE p."id" = $1`, postID)
	p, err := scanPostRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return postRow{}, ViewerContext{}, httpx.NotFoundMessage("Post not found.")
		}
		return postRow{}, ViewerContext{}, fmt.Errorf("get post: %w", err)
	}
	if p.DeletedAt != nil {
		return postRow{}, ViewerContext{}, httpx.NotFoundMessage("Post not found.")
	}
	vctx, err := LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return postRow{}, ViewerContext{}, err
	}
	access := PostAccess{
		AuthorID: p.AuthorID, UniversityID: p.UniversityID,
		CommunityID: p.CommunityID, Visibility: p.Visibility, DeletedAt: p.DeletedAt,
	}
	if !CanViewPost(vctx, access) {
		return postRow{}, ViewerContext{}, httpx.NotFoundMessage("Post not found.")
	}
	return p, vctx, nil
}

func (s *Service) likedBy(ctx context.Context, viewerID, postID string) (bool, error) {
	var count int
	err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "Like" WHERE "userId" = $1 AND "postId" = $2`,
		viewerID, postID).Scan(&count)
	return count > 0, err
}

// pagedPosts runs a paginated post list applying the visibility filter.
type CreatePostRequest struct {
	Content     string
	Visibility  string
	CommunityID *string
	EventID     *string
}

type UpdatePostRequest struct {
	Content    *string
	Visibility *string
}

// loadPostRow re-reads a post with its author and gated event summary.
func (s *Service) loadPostRow(ctx context.Context, postID string) (postRow, error) {
	row := s.db.Pool.QueryRow(ctx, `
		SELECT `+postColumns+`
		`+postJoins+`
		WHERE p."id" = $1`, postID)
	p, err := scanPostRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return postRow{}, httpx.NotFoundMessage("Post not found.")
		}
		return postRow{}, fmt.Errorf("get post: %w", err)
	}
	return p, nil
}

// ------------------------------------------------------------------ Create

func (s *Service) Create(ctx context.Context, authorID string, req CreatePostRequest) (PostView, error) {
	membership, err := s.myActiveMembership(ctx, authorID)
	if err != nil {
		return PostView{}, err
	}
	if membership.UniversityID == "" || !membership.UniversityActive {
		return PostView{}, httpx.Forbidden("Join an active university before posting.")
	}

	var communityID *string
	if req.CommunityID != nil {
		var universityID string
		var isActive bool
		switch err := s.db.Pool.QueryRow(ctx,
			`SELECT "universityId","isActive" FROM "Community" WHERE "id" = $1`,
			*req.CommunityID).Scan(&universityID, &isActive); {
		case errors.Is(err, pgx.ErrNoRows):
			return PostView{}, httpx.NotFoundMessage("Community not found.")
		case err != nil:
			return PostView{}, fmt.Errorf("get community: %w", err)
		case !isActive:
			return PostView{}, httpx.NotFoundMessage("Community not found.")
		case universityID != membership.UniversityID:
			return PostView{}, httpx.Forbidden("Communities must belong to your own university.")
		}
		var status string
		switch err := s.db.Pool.QueryRow(ctx,
			`SELECT "status" FROM "CommunityMembership" WHERE "userId" = $1 AND "communityId" = $2`,
			authorID, *req.CommunityID).Scan(&status); {
		case errors.Is(err, pgx.ErrNoRows):
			return PostView{}, httpx.Forbidden("Join the community before posting into it.")
		case err != nil:
			return PostView{}, fmt.Errorf("get community membership: %w", err)
		case status != "ACTIVE":
			return PostView{}, httpx.Forbidden("Join the community before posting into it.")
		}
		communityID = req.CommunityID
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = "UNIVERSITY"
	}
	if visibility == "COMMUNITY" && communityID == nil {
		return PostView{}, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError,
			"Community posts require a community.")
	}

	eventID, err := s.resolveEvent(ctx, authorID, membership.UniversityID, req.EventID)
	if err != nil {
		return PostView{}, err
	}

	now := s.now()
	postID := uuid.NewString()
	if err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO "Post" ("id","authorId","universityId","communityId","content","visibility","eventId","createdAt","updatedAt")
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
		RETURNING "id"`,
		postID, authorID, membership.UniversityID, communityID, req.Content, visibility, eventID, now,
	).Scan(&postID); err != nil {
		return PostView{}, fmt.Errorf("insert post: %w", err)
	}

	created, err := s.loadPostRow(ctx, postID)
	if err != nil {
		return PostView{}, err
	}
	vctx, err := LoadViewerContext(ctx, s.db, authorID)
	if err != nil {
		return PostView{}, err
	}
	return toPostView(created, false, vctx), nil
}

// eventRefRow is the slice of an Event row needed to validate a reference.
type eventRefRow struct {
	ID           string
	OrganizerID  string
	UniversityID string
	CommunityID  *string
	Status       string
}

// resolveEvent validates an optional event reference, returning the stored id.
func (s *Service) resolveEvent(ctx context.Context, authorID, universityID string, eventID *string) (*string, error) {
	if eventID == nil {
		return nil, nil
	}
	var e eventRefRow
	switch err := s.db.Pool.QueryRow(ctx, `
		SELECT "id","organizerId","universityId","communityId","status" FROM "Event" WHERE "id" = $1`,
		*eventID).Scan(&e.ID, &e.OrganizerID, &e.UniversityID, &e.CommunityID, &e.Status); {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, httpx.NotFoundMessage("Event not found.")
	case err != nil:
		return nil, fmt.Errorf("get event: %w", err)
	}
	if e.Status != "PUBLISHED" {
		return nil, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError,
			"Only published events can be referenced.")
	}
	if e.UniversityID != universityID {
		return nil, httpx.Forbidden("Events must belong to your own university.")
	}
	if e.CommunityID != nil {
		var status string
		switch err := s.db.Pool.QueryRow(ctx,
			`SELECT "status" FROM "CommunityMembership" WHERE "userId" = $1 AND "communityId" = $2`,
			authorID, *e.CommunityID).Scan(&status); {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, httpx.Forbidden("Community events require community membership.")
		case err != nil:
			return nil, fmt.Errorf("get event community membership: %w", err)
		case status != "ACTIVE":
			return nil, httpx.Forbidden("Community events require community membership.")
		}
	}
	var blocked int
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM "Block"
		WHERE ("blockerId" = $1 AND "blockedId" = $2) OR ("blockerId" = $2 AND "blockedId" = $1)`,
		authorID, e.OrganizerID).Scan(&blocked); err != nil {
		return nil, fmt.Errorf("check event block: %w", err)
	}
	if blocked > 0 {
		return nil, httpx.Forbidden("You cannot reference this event.")
	}
	return &e.ID, nil
}

// ------------------------------------------------------------------ Detail

func (s *Service) Detail(ctx context.Context, viewerID, postID string) (PostView, error) {
	p, vctx, err := s.gate(ctx, viewerID, postID)
	if err != nil {
		return PostView{}, err
	}
	liked, err := s.likedBy(ctx, viewerID, postID)
	if err != nil {
		return PostView{}, err
	}
	return toPostView(p, liked, vctx), nil
}

// ------------------------------------------------------------------ Update

func (s *Service) Update(ctx context.Context, viewerID, postID string, req UpdatePostRequest) (PostView, error) {
	p, vctx, err := s.gate(ctx, viewerID, postID)
	if err != nil {
		return PostView{}, err
	}
	if p.AuthorID != viewerID {
		return PostView{}, httpx.Forbidden("Only the author can update this post.")
	}
	sets := make([]string, 0, 2)
	args := []any{postID}
	if req.Content != nil {
		args = append(args, *req.Content)
		sets = append(sets, fmt.Sprintf(`"content" = $%d`, len(args)))
	}
	if req.Visibility != nil {
		if *req.Visibility == "COMMUNITY" && p.CommunityID == nil {
			return PostView{}, httpx.NewError(http.StatusBadRequest, httpx.CodeValidationError,
				"Community posts require a community.")
		}
		args = append(args, *req.Visibility)
		sets = append(sets, fmt.Sprintf(`"visibility" = $%d`, len(args)))
	}
	if len(sets) == 0 {
		liked, err := s.likedBy(ctx, viewerID, postID)
		if err != nil {
			return PostView{}, err
		}
		return toPostView(p, liked, vctx), nil
	}
	args = append(args, s.now())
	sets = append(sets, fmt.Sprintf(`"updatedAt" = $%d`, len(args)))
	if _, err := s.db.Pool.Exec(ctx,
		`UPDATE "Post" SET `+strings.Join(sets, ", ")+` WHERE "id" = $1`, args...); err != nil {
		return PostView{}, fmt.Errorf("update post: %w", err)
	}
	updated, err := s.loadPostRow(ctx, postID)
	if err != nil {
		return PostView{}, err
	}
	liked, err := s.likedBy(ctx, viewerID, postID)
	if err != nil {
		return PostView{}, err
	}
	return toPostView(updated, liked, vctx), nil
}

// ------------------------------------------------------------------ Remove

func (s *Service) Remove(ctx context.Context, viewerID, postID string) (map[string]bool, error) {
	p, _, err := s.gate(ctx, viewerID, postID)
	if err != nil {
		return nil, err
	}
	if p.AuthorID != viewerID {
		return nil, httpx.Forbidden("Only the author can delete this post.")
	}
	if _, err := s.db.Pool.Exec(ctx,
		`UPDATE "Post" SET "deletedAt" = $1 WHERE "id" = $2`, s.now(), postID); err != nil {
		return nil, fmt.Errorf("delete post: %w", err)
	}
	return map[string]bool{"deleted": true}, nil
}

// ------------------------------------------------------------------ Lists

func (s *Service) ListByAuthor(ctx context.Context, viewerID, authorID string, page httpx.Page) (httpx.PageResult[PostView], error) {
	if err := s.activeUserOrThrow(ctx, authorID); err != nil {
		return httpx.PageResult[PostView]{}, err
	}
	vctx, err := LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return httpx.PageResult[PostView]{}, err
	}
	if viewerID != authorID {
		if contains(vctx.BlockedIDs, authorID) {
			return httpx.PageResult[PostView]{Items: []PostView{}}, nil
		}
		return s.pagedPosts(ctx, viewerID, vctx, ` AND p."authorId" = $1`, []any{authorID}, page)
	}
	return s.pagedPostsOwn(ctx, authorID, vctx, page)
}

// buildVisOR renders the visibility OR branches, appending their arguments to
// existingArgs and numbering placeholders from len(existingArgs).
func buildVisOR(vctx ViewerContext, existingArgs []any) (string, []any) {
	args := existingArgs
	clause := `(p."visibility" = 'PUBLIC'`
	if len(vctx.UniversityIDs) > 0 {
		clause += ` OR (p."visibility" = 'UNIVERSITY' AND p."universityId" IN (`
		for i, id := range vctx.UniversityIDs {
			args = append(args, id)
			if i > 0 {
				clause += ","
			}
			clause += fmt.Sprintf("$%d", len(args))
		}
		clause += `))`
	}
	if len(vctx.CommunityIDs) > 0 {
		clause += ` OR (p."visibility" = 'COMMUNITY' AND p."communityId" IN (`
		for i, id := range vctx.CommunityIDs {
			args = append(args, id)
			if i > 0 {
				clause += ","
			}
			clause += fmt.Sprintf("$%d", len(args))
		}
		clause += `))`
	}
	if len(vctx.FollowingIDs) > 0 {
		clause += ` OR (p."visibility" = 'FOLLOWERS' AND p."authorId" IN (`
		for i, id := range vctx.FollowingIDs {
			args = append(args, id)
			if i > 0 {
				clause += ","
			}
			clause += fmt.Sprintf("$%d", len(args))
		}
		clause += `))`
	}
	clause += `)`
	return clause, args
}

func (s *Service) pagedPosts(ctx context.Context, viewerID string, vctx ViewerContext,
	extraWhere string, extraArgs []any, page httpx.Page) (httpx.PageResult[PostView], error) {

	args := append([]any{}, extraArgs...)
	visOr, args := buildVisOR(vctx, args)

	blockedWhere := ""
	if len(vctx.BlockedIDs) > 0 {
		blockedWhere = ` AND p."authorId" NOT IN (`
		for i, id := range vctx.BlockedIDs {
			if i > 0 {
				blockedWhere += ","
			}
			args = append(args, id)
			blockedWhere += fmt.Sprintf("$%d", len(args))
		}
		blockedWhere += `)`
	}

	cursorWhere := ""
	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		n := len(args)
		cursorWhere = fmt.Sprintf(` AND (p."createdAt" < $%d OR (p."createdAt" = $%d AND p."id" < $%d))`, n-1, n-1, n)
	}

	args = append(args, page.Limit+1)
	limitN := fmt.Sprintf("$%d", len(args))

	query := `SELECT ` + postColumns + `
		` + postJoins + `
		WHERE p."deletedAt" IS NULL` + extraWhere + blockedWhere + ` AND ` + visOr + cursorWhere +
		` ORDER BY p."createdAt" DESC, p."id" DESC LIMIT ` + limitN

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[PostView]{}, fmt.Errorf("list posts: %w", err)
	}
	defer rows.Close()

	var prows []postRow
	for rows.Next() {
		p, err := scanPostRow(rows)
		if err != nil {
			return httpx.PageResult[PostView]{}, fmt.Errorf("scan post: %w", err)
		}
		prows = append(prows, p)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[PostView]{}, fmt.Errorf("iterate posts: %w", err)
	}

	paged := httpx.BuildPage(prows, page.Limit, func(p postRow) (time.Time, string) {
		return p.CreatedAt, p.ID
	})

	if len(paged.Items) == 0 {
		return httpx.PageResult[PostView]{Items: []PostView{}}, nil
	}
	liked, err := s.likedByPage(ctx, viewerID, paged.Items)
	if err != nil {
		return httpx.PageResult[PostView]{}, err
	}

	items := make([]PostView, 0, len(paged.Items))
	for _, p := range paged.Items {
		items = append(items, toPostView(p, liked[p.ID], vctx))
	}
	return httpx.PageResult[PostView]{Items: items, NextCursor: paged.NextCursor}, nil
}

func (s *Service) pagedPostsOwn(ctx context.Context, authorID string, vctx ViewerContext, page httpx.Page) (httpx.PageResult[PostView], error) {
	args := []any{authorID}
	cursorWhere := ""
	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		cursorWhere = ` AND (p."createdAt" < $2 OR (p."createdAt" = $2 AND p."id" < $3))`
	}
	args = append(args, page.Limit+1)

	query := `SELECT ` + postColumns + `
		` + postJoins + `
		WHERE p."authorId" = $1 AND p."deletedAt" IS NULL` + cursorWhere +
		fmt.Sprintf(` ORDER BY p."createdAt" DESC, p."id" DESC LIMIT $%d`, len(args))

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[PostView]{}, fmt.Errorf("list own posts: %w", err)
	}
	defer rows.Close()
	var prows []postRow
	for rows.Next() {
		p, err := scanPostRow(rows)
		if err != nil {
			return httpx.PageResult[PostView]{}, fmt.Errorf("scan post: %w", err)
		}
		prows = append(prows, p)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[PostView]{}, fmt.Errorf("iterate posts: %w", err)
	}

	paged := httpx.BuildPage(prows, page.Limit, func(p postRow) (time.Time, string) {
		return p.CreatedAt, p.ID
	})
	if len(paged.Items) == 0 {
		return httpx.PageResult[PostView]{Items: []PostView{}}, nil
	}
	liked, err := s.likedByPage(ctx, vctx.UserID, paged.Items)
	if err != nil {
		return httpx.PageResult[PostView]{}, err
	}
	items := make([]PostView, 0, len(paged.Items))
	for _, p := range paged.Items {
		items = append(items, toPostView(p, liked[p.ID], vctx))
	}
	return httpx.PageResult[PostView]{Items: items, NextCursor: paged.NextCursor}, nil
}

// likedByPage reports, for one page of posts, which ones the viewer has liked.
func (s *Service) likedByPage(ctx context.Context, viewerID string, rows []postRow) (map[string]bool, error) {
	if len(rows) == 0 {
		return map[string]bool{}, nil
	}
	args := make([]any, 0, len(rows)+1)
	args = append(args, viewerID)
	in := ""
	for i, p := range rows {
		if i > 0 {
			in += ","
		}
		args = append(args, p.ID)
		in += fmt.Sprintf("$%d", len(args))
	}
	qrows, err := s.db.Pool.Query(ctx,
		`SELECT "postId" FROM "Like" WHERE "userId" = $1 AND "postId" IN (`+in+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("load likes: %w", err)
	}
	defer qrows.Close()
	liked := map[string]bool{}
	for qrows.Next() {
		var pid string
		if err := qrows.Scan(&pid); err != nil {
			return nil, fmt.Errorf("scan like: %w", err)
		}
		liked[pid] = true
	}
	return liked, qrows.Err()
}

// ------------------------------------------------------------------ List by university

func (s *Service) ListByUniversity(ctx context.Context, viewerID, slug string, page httpx.Page) (httpx.PageResult[PostView], error) {
	var uniID string
	var uniActive bool
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id","isActive" FROM "University" WHERE "slug" = $1`, slug).Scan(&uniID, &uniActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.PageResult[PostView]{}, httpx.NotFoundMessage("University not found.")
		}
		return httpx.PageResult[PostView]{}, fmt.Errorf("get university: %w", err)
	}
	if !uniActive {
		return httpx.PageResult[PostView]{}, httpx.NotFoundMessage("University not found.")
	}

	vctx, err := LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return httpx.PageResult[PostView]{}, err
	}
	if !contains(vctx.UniversityIDs, uniID) {
		return httpx.PageResult[PostView]{}, httpx.Forbidden("University posts are restricted to its members.")
	}

	extraArgs := []any{uniID}
	return s.pagedPosts(ctx, viewerID, vctx, ` AND p."universityId" = $1`, extraArgs, page)
}

// ------------------------------------------------------------------ List by community

func (s *Service) ListByCommunity(ctx context.Context, viewerID, communityID string, page httpx.Page) (httpx.PageResult[PostView], error) {
	var comVis string
	var comActive bool
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "visibility","isActive" FROM "Community" WHERE "id" = $1`, communityID).Scan(&comVis, &comActive)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.PageResult[PostView]{}, httpx.NotFoundMessage("Community not found.")
		}
		return httpx.PageResult[PostView]{}, fmt.Errorf("get community: %w", err)
	}
	if !comActive {
		return httpx.PageResult[PostView]{}, httpx.NotFoundMessage("Community not found.")
	}

	vctx, err := LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return httpx.PageResult[PostView]{}, err
	}
	if comVis == "PRIVATE" && !contains(vctx.CommunityIDs, communityID) {
		return httpx.PageResult[PostView]{}, httpx.Forbidden("This community is private.")
	}

	extraArgs := []any{communityID}
	return s.pagedPosts(ctx, viewerID, vctx, ` AND p."communityId" = $1`, extraArgs, page)
}

// ------------------------------------------------------------------ Like

func (s *Service) Like(ctx context.Context, viewerID, postID string) (map[string]any, error) {
	p, _, err := s.gate(ctx, viewerID, postID)
	if err != nil {
		return nil, err
	}

	// Idempotent: already liked?
	var count int
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "Like" WHERE "userId" = $1 AND "postId" = $2`,
		viewerID, postID).Scan(&count); err != nil {
		return nil, fmt.Errorf("check like: %w", err)
	}
	if count > 0 {
		return map[string]any{"liked": true, "likeCount": p.LikeCount}, nil
	}

	now := s.now()
	likeID := uuid.NewString()
	eventID := uuid.NewString()
	var newLikeCount int

	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO "Like" ("id","userId","postId","createdAt")
			VALUES ($1,$2,$3,$4)`,
			likeID, viewerID, postID, now)
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errDuplicateLike
			}
			return fmt.Errorf("insert like: %w", err)
		}
		if err := tx.QueryRow(ctx, `
			UPDATE "Post" SET "likeCount" = "likeCount" + 1 WHERE "id" = $1 RETURNING "likeCount"`,
			postID).Scan(&newLikeCount); err != nil {
			return fmt.Errorf("increment like count: %w", err)
		}
		payload := fmt.Sprintf(`{"postId":%q,"likeId":%q}`, postID, likeID)
		_, err = tx.Exec(ctx, `
			INSERT INTO "DomainEvent" ("id","type","aggregateType","aggregateId","actorId","payload","status","attempts","availableAt","createdAt","updatedAt")
			VALUES ($1,'post.liked','Post',$2,$3,$4::jsonb,'PENDING',0,$5,$5,$5)`,
			eventID, postID, viewerID, payload, now)
		return err
	})
	if err != nil {
		if errors.Is(err, errDuplicateLike) {
			// Concurrent double-like: the transaction above is aborted, so read
			// the winner's state on a fresh connection, as the reference does.
			if err := s.db.Pool.QueryRow(ctx,
				`SELECT "likeCount" FROM "Post" WHERE "id" = $1`, postID).Scan(&newLikeCount); err != nil {
				return nil, fmt.Errorf("read like count: %w", err)
			}
			return map[string]any{"liked": true, "likeCount": newLikeCount}, nil
		}
		return nil, err
	}
	return map[string]any{"liked": true, "likeCount": newLikeCount}, nil
}

var errDuplicateLike = errors.New("duplicate like")

// ------------------------------------------------------------------ Unlike

func (s *Service) Unlike(ctx context.Context, viewerID, postID string) (map[string]any, error) {
	_, _, err := s.gate(ctx, viewerID, postID)
	if err != nil {
		return nil, err
	}

	var likeID string
	err = s.db.Pool.QueryRow(ctx,
		`SELECT "id" FROM "Like" WHERE "userId" = $1 AND "postId" = $2`,
		viewerID, postID).Scan(&likeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			var lc int
			_ = s.db.Pool.QueryRow(ctx, `SELECT "likeCount" FROM "Post" WHERE "id" = $1`, postID).Scan(&lc)
			return map[string]any{"liked": false, "likeCount": lc}, nil
		}
		return nil, fmt.Errorf("get like: %w", err)
	}

	var newLikeCount int
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM "Like" WHERE "id" = $1`, likeID); err != nil {
			return fmt.Errorf("delete like: %w", err)
		}
		return tx.QueryRow(ctx, `
			UPDATE "Post" SET "likeCount" = GREATEST(0, "likeCount" - 1) WHERE "id" = $1 RETURNING "likeCount"`,
			postID).Scan(&newLikeCount)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"liked": false, "likeCount": newLikeCount}, nil
}

func commentViewOf(c commentRow) CommentView {
	return CommentView{
		ID:        c.ID,
		PostID:    c.PostID,
		AuthorID:  c.AuthorID,
		Author:    PostAuthor{ID: c.AuthorID, DisplayName: c.DisplayName, AvatarURL: c.AvatarURL},
		Content:   c.Content,
		DeletedAt: c.DeletedAt,
		CreatedAt: c.CreatedAt,
		UpdatedAt: c.UpdatedAt,
	}
}

// ------------------------------------------------------------------ Create comment

type CreateCommentRequest struct {
	Content string
}

func (s *Service) CreateComment(ctx context.Context, viewerID, postID string, req CreateCommentRequest) (CommentView, error) {
	p, _, err := s.gate(ctx, viewerID, postID)
	_ = p
	if err != nil {
		return CommentView{}, err
	}

	now := s.now()
	commentID := uuid.NewString()
	eventID := uuid.NewString()

	var c commentRow
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO "Comment" ("id","postId","authorId","content","createdAt","updatedAt")
			VALUES ($1,$2,$3,$4,$5,$5)
			RETURNING "id","postId","authorId","content","deletedAt","createdAt","updatedAt",
			          (SELECT "displayName" FROM "User" WHERE "id" = $3),
			          (SELECT "avatarUrl" FROM "User" WHERE "id" = $3)`,
			commentID, postID, viewerID, req.Content, now,
		).Scan(&c.ID, &c.PostID, &c.AuthorID, &c.Content, &c.DeletedAt, &c.CreatedAt, &c.UpdatedAt, &c.DisplayName, &c.AvatarURL)
		if err != nil {
			return fmt.Errorf("insert comment: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE "Post" SET "commentCount" = "commentCount" + 1 WHERE "id" = $1`, postID); err != nil {
			return fmt.Errorf("increment comment count: %w", err)
		}
		payload := fmt.Sprintf(`{"postId":%q,"commentId":%q}`, postID, commentID)
		_, err = tx.Exec(ctx, `
			INSERT INTO "DomainEvent" ("id","type","aggregateType","aggregateId","actorId","payload","status","attempts","availableAt","createdAt","updatedAt")
			VALUES ($1,'post.commented','Post',$2,$3,$4::jsonb,'PENDING',0,$5,$5,$5)`,
			eventID, postID, viewerID, payload, now)
		return err
	})
	if err != nil {
		return CommentView{}, err
	}
	return commentViewOf(c), nil
}

// ------------------------------------------------------------------ List comments

func (s *Service) ListComments(ctx context.Context, viewerID, postID string, page httpx.Page) (httpx.PageResult[CommentView], error) {
	if _, _, err := s.gate(ctx, viewerID, postID); err != nil {
		return httpx.PageResult[CommentView]{}, err
	}

	args := []any{postID}
	cursorWhere := ""
	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		cursorWhere = ` AND (c."createdAt" > $2 OR (c."createdAt" = $2 AND c."id" > $3))`
	}
	args = append(args, page.Limit+1)

	query := `SELECT c."id", c."postId", c."authorId", c."content", c."deletedAt", c."createdAt", c."updatedAt",
	                 u."displayName", u."avatarUrl"
	          FROM "Comment" c
	          JOIN "User" u ON u."id" = c."authorId"
	          WHERE c."postId" = $1 AND c."deletedAt" IS NULL` + cursorWhere +
		fmt.Sprintf(` ORDER BY c."createdAt" ASC, c."id" ASC LIMIT $%d`, len(args))

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[CommentView]{}, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	var crows []commentRow
	for rows.Next() {
		var c commentRow
		if err := rows.Scan(&c.ID, &c.PostID, &c.AuthorID, &c.Content, &c.DeletedAt, &c.CreatedAt, &c.UpdatedAt, &c.DisplayName, &c.AvatarURL); err != nil {
			return httpx.PageResult[CommentView]{}, fmt.Errorf("scan comment: %w", err)
		}
		crows = append(crows, c)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[CommentView]{}, fmt.Errorf("iterate comments: %w", err)
	}

	paged := httpx.BuildPage(crows, page.Limit, func(c commentRow) (time.Time, string) {
		return c.CreatedAt, c.ID
	})
	items := make([]CommentView, 0, len(paged.Items))
	for _, c := range paged.Items {
		items = append(items, commentViewOf(c))
	}
	return httpx.PageResult[CommentView]{Items: items, NextCursor: paged.NextCursor}, nil
}

// ------------------------------------------------------------------ Update comment

type UpdateCommentRequest struct {
	Content string
}

func (s *Service) UpdateComment(ctx context.Context, viewerID, commentID string, req UpdateCommentRequest) (CommentView, error) {
	var c commentRow
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id","postId","authorId","content","deletedAt","createdAt","updatedAt" FROM "Comment" WHERE "id" = $1`,
		commentID).Scan(&c.ID, &c.PostID, &c.AuthorID, &c.Content, &c.DeletedAt, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CommentView{}, httpx.NotFoundMessage("Comment not found.")
		}
		return CommentView{}, fmt.Errorf("get comment: %w", err)
	}
	if c.DeletedAt != nil || c.AuthorID != viewerID {
		return CommentView{}, httpx.NotFoundMessage("Comment not found.")
	}

	now := s.now()
	var updated commentRow
	err = s.db.Pool.QueryRow(ctx, `
		UPDATE "Comment" SET "content" = $1, "updatedAt" = $2 WHERE "id" = $3
		RETURNING "id","postId","authorId","content","deletedAt","createdAt","updatedAt",
		          (SELECT "displayName" FROM "User" WHERE "id" = "authorId"),
		          (SELECT "avatarUrl" FROM "User" WHERE "id" = "authorId")`,
		req.Content, now, commentID,
	).Scan(&updated.ID, &updated.PostID, &updated.AuthorID, &updated.Content, &updated.DeletedAt, &updated.CreatedAt, &updated.UpdatedAt, &updated.DisplayName, &updated.AvatarURL)
	if err != nil {
		return CommentView{}, fmt.Errorf("update comment: %w", err)
	}
	return commentViewOf(updated), nil
}

// ------------------------------------------------------------------ Delete comment

func (s *Service) DeleteComment(ctx context.Context, viewerID, commentID string) (map[string]bool, error) {
	var c commentRow
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "id","postId","authorId","deletedAt" FROM "Comment" WHERE "id" = $1`,
		commentID).Scan(&c.ID, &c.PostID, &c.AuthorID, &c.DeletedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, httpx.NotFoundMessage("Comment not found.")
		}
		return nil, fmt.Errorf("get comment: %w", err)
	}
	if c.DeletedAt != nil {
		return nil, httpx.NotFoundMessage("Comment not found.")
	}

	// Get post author for permission check.
	var postAuthorID string
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT "authorId" FROM "Post" WHERE "id" = $1`, c.PostID).Scan(&postAuthorID); err != nil {
		return nil, fmt.Errorf("get post author: %w", err)
	}

	isCommentAuthor := c.AuthorID == viewerID
	isPostOwner := postAuthorID == viewerID
	if !isCommentAuthor && !isPostOwner {
		return nil, httpx.Forbidden("Only the author or the post owner can delete this comment.")
	}

	now := s.now()
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE "Comment" SET "deletedAt" = $1 WHERE "id" = $2`, now, commentID); err != nil {
			return fmt.Errorf("soft-delete comment: %w", err)
		}
		_, err := tx.Exec(ctx,
			`UPDATE "Post" SET "commentCount" = GREATEST(0, "commentCount" - 1) WHERE "id" = $1`, c.PostID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}
