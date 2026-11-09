package feed

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/events"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/posts"

	"github.com/jackc/pgx/v5"
)

const candidateLimit = 200

type rawPost struct {
	ID           string
	AuthorID     string
	AuthorName   *string
	AuthorAvatar *string
	UniversityID string
	CommunityID  *string
	Content      string
	Visibility   string
	LikeCount    int
	CommentCount int
	DeletedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type rawEvent struct {
	ID                   string
	OrganizerID          string
	OrganizerName        *string
	OrganizerAvatar      *string
	UniversityID         string
	CommunityID          *string
	Title                string
	Description          *string
	Type                 string
	Location             *string
	StartsAt             time.Time
	EndsAt               *time.Time
	Capacity             int
	RegisteredCount      int
	RegistrationDeadline *time.Time
	Visibility           string
	Status               string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// Service handles home, university, community and explore feeds.
type Service struct {
	db *database.DB
}

// NewService constructs a feed Service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

func (s *Service) HomeFeed(ctx context.Context, viewerID string, limit int, cursorRaw *string) (FeedPage, error) {
	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return FeedPage{}, fmt.Errorf("load viewer context: %w", err)
	}

	authorIDs := append([]string{viewerID}, vctx.FollowingIDs...)

	byAuthors, err := s.fetchPostsByAuthors(ctx, authorIDs)
	if err != nil {
		return FeedPage{}, err
	}
	byUnis, err := s.fetchPostsForUniversities(ctx, vctx.UniversityIDs)
	if err != nil {
		return FeedPage{}, err
	}
	byComms, err := s.fetchPostsForCommunities(ctx, vctx.CommunityIDs)
	if err != nil {
		return FeedPage{}, err
	}
	pubPosts, err := s.fetchPublicPosts(ctx, 100)
	if err != nil {
		return FeedPage{}, err
	}

	uniEvents, err := s.fetchEventsForUniversities(ctx, vctx.UniversityIDs)
	if err != nil {
		return FeedPage{}, err
	}
	commEvents, err := s.fetchEventsForCommunities(ctx, vctx.CommunityIDs)
	if err != nil {
		return FeedPage{}, err
	}

	allPosts := dedupePosts(byAuthors, byUnis, byComms, pubPosts)
	allEvents := dedupeEvents(uniEvents, commEvents)

	return s.assemble(ctx, vctx, allPosts, allEvents, limit, cursorRaw)
}

func (s *Service) UniversityFeed(ctx context.Context, viewerID string, targetUniID *string, limit int, cursorRaw *string) (FeedPage, error) {
	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return FeedPage{}, fmt.Errorf("load viewer context: %w", err)
	}

	target := ""
	if targetUniID != nil && *targetUniID != "" {
		target = *targetUniID
	} else if len(vctx.UniversityIDs) > 0 {
		target = vctx.UniversityIDs[0]
	} else {
		return FeedPage{}, httpx.Forbidden("Join a university to view its feed.")
	}

	var isActive bool
	err = s.db.Pool.QueryRow(ctx, `SELECT "isActive" FROM "University" WHERE "id" = $1`, target).Scan(&isActive)
	if errors.Is(err, pgx.ErrNoRows) || !isActive {
		return FeedPage{}, httpx.NewError(404, httpx.CodeNotFound, "University not found.")
	}
	if err != nil {
		return FeedPage{}, fmt.Errorf("query university: %w", err)
	}

	isMember := false
	for _, u := range vctx.UniversityIDs {
		if u == target {
			isMember = true
			break
		}
	}
	if !isMember {
		return FeedPage{}, httpx.Forbidden("University feeds are restricted to its members.")
	}

	allPosts, err := s.fetchPostsForUniversities(ctx, []string{target})
	if err != nil {
		return FeedPage{}, err
	}
	allEvents, err := s.fetchEventsForUniversities(ctx, []string{target})
	if err != nil {
		return FeedPage{}, err
	}

	return s.assemble(ctx, vctx, allPosts, allEvents, limit, cursorRaw)
}

func (s *Service) CommunityFeed(ctx context.Context, viewerID, communityID string, limit int, cursorRaw *string) (FeedPage, error) {
	var visibility string
	var isActive bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "visibility", "isActive" FROM "Community" WHERE "id" = $1`, communityID,
	).Scan(&visibility, &isActive)
	if errors.Is(err, pgx.ErrNoRows) || !isActive {
		return FeedPage{}, httpx.NewError(404, httpx.CodeNotFound, "Community not found.")
	}
	if err != nil {
		return FeedPage{}, fmt.Errorf("query community: %w", err)
	}

	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return FeedPage{}, fmt.Errorf("load viewer context: %w", err)
	}

	if visibility == "PRIVATE" {
		isMember := false
		for _, c := range vctx.CommunityIDs {
			if c == communityID {
				isMember = true
				break
			}
		}
		if !isMember {
			return FeedPage{}, httpx.Forbidden("This community is private.")
		}
	}

	allPosts, err := s.fetchPostsForCommunities(ctx, []string{communityID})
	if err != nil {
		return FeedPage{}, err
	}
	allEvents, err := s.fetchEventsForCommunities(ctx, []string{communityID})
	if err != nil {
		return FeedPage{}, err
	}

	return s.assemble(ctx, vctx, allPosts, allEvents, limit, cursorRaw)
}

func (s *Service) ExploreFeed(ctx context.Context, viewerID string, limit int, cursorRaw *string) (FeedPage, error) {
	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return FeedPage{}, fmt.Errorf("load viewer context: %w", err)
	}

	allPosts, err := s.fetchPublicPosts(ctx, 100)
	if err != nil {
		return FeedPage{}, err
	}
	allEvents, err := s.fetchPublicEvents(ctx, 100)
	if err != nil {
		return FeedPage{}, err
	}

	return s.assemble(ctx, vctx, allPosts, allEvents, limit, cursorRaw)
}

func (s *Service) assemble(
	ctx context.Context,
	vctx posts.ViewerContext,
	candidatePosts []rawPost,
	candidateEvents []rawEvent,
	limit int,
	cursorRaw *string,
) (FeedPage, error) {
	var cursor *FeedCursor
	if cursorRaw != nil && *cursorRaw != "" {
		c, err := DecodeCursor(*cursorRaw)
		if err != nil {
			return FeedPage{}, err
		}
		cursor = c
	}

	// 1. Filter posts
	var visPosts []rawPost
	for _, p := range candidatePosts {
		if posts.CanViewPost(vctx, posts.PostAccess{
			AuthorID:     p.AuthorID,
			UniversityID: p.UniversityID,
			CommunityID:  p.CommunityID,
			Visibility:   p.Visibility,
			DeletedAt:    p.DeletedAt,
		}) {
			visPosts = append(visPosts, p)
		}
	}

	// 2. Filter events
	var visEvents []rawEvent
	for _, e := range candidateEvents {
		if eventVisible(vctx, e) {
			visEvents = append(visEvents, e)
		}
	}

	// 3. Stubs with scores
	type feedStub struct {
		isPost    bool
		postRow   rawPost
		eventRow  rawEvent
		id        string
		createdAt time.Time
		score     float64
	}

	var stubs []feedStub
	for _, p := range visPosts {
		stubs = append(stubs, feedStub{
			isPost:    true,
			postRow:   p,
			id:        p.ID,
			createdAt: p.CreatedAt,
			score:     ScorePost(p.CreatedAt, p.LikeCount, p.CommentCount),
		})
	}
	for _, e := range visEvents {
		stubs = append(stubs, feedStub{
			isPost:    false,
			eventRow:  e,
			id:        e.ID,
			createdAt: e.CreatedAt,
			score:     ScoreEvent(e.CreatedAt, e.RegisteredCount),
		})
	}

	// 4. Rank: score DESC, createdAt DESC, id DESC
	sort.Slice(stubs, func(i, j int) bool {
		a, b := stubs[i], stubs[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if !a.createdAt.Equal(b.createdAt) {
			return a.createdAt.After(b.createdAt)
		}
		return a.id > b.id
	})

	// 5. Paginate with keyset
	var after []feedStub
	if cursor != nil {
		for _, item := range stubs {
			itemCreatedAt := item.createdAt.UTC().Truncate(time.Millisecond)
			curCreatedAt := cursor.CreatedAt.UTC().Truncate(time.Millisecond)

			if item.score < cursor.Score ||
				(item.score == cursor.Score && (itemCreatedAt.Before(curCreatedAt) ||
					(itemCreatedAt.Equal(curCreatedAt) && item.id < cursor.ID))) {
				after = append(after, item)
			}
		}
	} else {
		after = stubs
	}

	take := limit
	if len(after) < take {
		take = len(after)
	}
	pageStubs := after[:take]

	// 6. Present items
	// Batch load likes for visible posts
	var postIDs []string
	for _, ps := range pageStubs {
		if ps.isPost {
			postIDs = append(postIDs, ps.id)
		}
	}

	likedSet := make(map[string]bool)
	if len(postIDs) > 0 {
		rows, err := s.db.Pool.Query(ctx, `
			SELECT "postId" FROM "Like" WHERE "userId" = $1 AND "postId" = ANY($2)`,
			vctx.UserID, postIDs,
		)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var pID string
				if err := rows.Scan(&pID); err == nil {
					likedSet[pID] = true
				}
			}
		}
	}

	items := make([]FeedItem, 0, len(pageStubs))
	for _, ps := range pageStubs {
		if ps.isPost {
			p := ps.postRow
			pv := posts.PostView{
				ID: p.ID,
				Author: posts.PostAuthor{
					ID:          p.AuthorID,
					DisplayName: p.AuthorName,
					AvatarURL:   p.AuthorAvatar,
				},
				UniversityID: p.UniversityID,
				CommunityID:  p.CommunityID,
				Content:      p.Content,
				Visibility:   p.Visibility,
				LikeCount:    p.LikeCount,
				CommentCount: p.CommentCount,
				LikedByMe:    likedSet[p.ID],
				Event:        nil,
				CreatedAt:    p.CreatedAt,
				UpdatedAt:    p.UpdatedAt,
			}
			items = append(items, FeedItem{
				Type:      FeedItemPost,
				ID:        p.ID,
				CreatedAt: p.CreatedAt,
				Score:     ps.score,
				Post:      &pv,
			})
		} else {
			e := ps.eventRow
			avail := e.Capacity - e.RegisteredCount
			if avail < 0 {
				avail = 0
			}
			ev := events.EventView{
				ID: e.ID,
				Organizer: events.EventOrganizer{
					ID:          e.OrganizerID,
					DisplayName: e.OrganizerName,
					AvatarURL:   e.OrganizerAvatar,
				},
				UniversityID:         e.UniversityID,
				CommunityID:          e.CommunityID,
				Title:                e.Title,
				Description:          e.Description,
				Type:                 e.Type,
				Location:             e.Location,
				StartsAt:             e.StartsAt,
				EndsAt:               e.EndsAt,
				Capacity:             e.Capacity,
				RegisteredCount:      e.RegisteredCount,
				AvailableSpots:       avail,
				RegistrationDeadline: e.RegistrationDeadline,
				Visibility:           e.Visibility,
				Status:               e.Status,
				CreatedAt:            e.CreatedAt,
				UpdatedAt:            e.UpdatedAt,
			}
			items = append(items, FeedItem{
				Type:      FeedItemEvent,
				ID:        e.ID,
				CreatedAt: e.CreatedAt,
				Score:     ps.score,
				Event:     &ev,
			})
		}
	}

	var nextCursor *string
	if len(after) > limit && len(pageStubs) > 0 {
		last := pageStubs[len(pageStubs)-1]
		enc := EncodeCursor(last.score, last.createdAt, last.id)
		nextCursor = &enc
	}

	return FeedPage{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

func eventVisible(ctx posts.ViewerContext, e rawEvent) bool {
	if e.OrganizerID == ctx.UserID {
		return true
	}
	if e.Status != "PUBLISHED" {
		return false
	}
	for _, b := range ctx.BlockedIDs {
		if b == e.OrganizerID {
			return false
		}
	}
	switch e.Visibility {
	case "PUBLIC":
		return true
	case "UNIVERSITY":
		for _, u := range ctx.UniversityIDs {
			if u == e.UniversityID {
				return true
			}
		}
		return false
	case "COMMUNITY":
		if e.CommunityID == nil {
			return false
		}
		for _, c := range ctx.CommunityIDs {
			if c == *e.CommunityID {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func dedupePosts(lists ...[]rawPost) []rawPost {
	seen := make(map[string]bool)
	var out []rawPost
	for _, list := range lists {
		for _, item := range list {
			if !seen[item.ID] {
				seen[item.ID] = true
				out = append(out, item)
			}
		}
	}
	return out
}

func dedupeEvents(lists ...[]rawEvent) []rawEvent {
	seen := make(map[string]bool)
	var out []rawEvent
	for _, list := range lists {
		for _, item := range list {
			if !seen[item.ID] {
				seen[item.ID] = true
				out = append(out, item)
			}
		}
	}
	return out
}

func (s *Service) fetchPostsByAuthors(ctx context.Context, authorIDs []string) ([]rawPost, error) {
	if len(authorIDs) == 0 {
		return nil, nil
	}
	return s.scanPosts(ctx, `
		SELECT p."id", p."authorId", u."displayName", u."avatarUrl", p."universityId", p."communityId",
		       p."content", p."visibility", p."likeCount", p."commentCount", p."deletedAt", p."createdAt", p."updatedAt"
		FROM "Post" p
		JOIN "User" u ON u."id" = p."authorId"
		WHERE p."authorId" = ANY($1) AND p."deletedAt" IS NULL
		ORDER BY p."createdAt" DESC, p."id" DESC
		LIMIT $2`, authorIDs, candidateLimit)
}

func (s *Service) fetchPostsForUniversities(ctx context.Context, uniIDs []string) ([]rawPost, error) {
	if len(uniIDs) == 0 {
		return nil, nil
	}
	return s.scanPosts(ctx, `
		SELECT p."id", p."authorId", u."displayName", u."avatarUrl", p."universityId", p."communityId",
		       p."content", p."visibility", p."likeCount", p."commentCount", p."deletedAt", p."createdAt", p."updatedAt"
		FROM "Post" p
		JOIN "User" u ON u."id" = p."authorId"
		WHERE p."universityId" = ANY($1) AND p."deletedAt" IS NULL
		ORDER BY p."createdAt" DESC, p."id" DESC
		LIMIT $2`, uniIDs, candidateLimit)
}

func (s *Service) fetchPostsForCommunities(ctx context.Context, commIDs []string) ([]rawPost, error) {
	if len(commIDs) == 0 {
		return nil, nil
	}
	return s.scanPosts(ctx, `
		SELECT p."id", p."authorId", u."displayName", u."avatarUrl", p."universityId", p."communityId",
		       p."content", p."visibility", p."likeCount", p."commentCount", p."deletedAt", p."createdAt", p."updatedAt"
		FROM "Post" p
		JOIN "User" u ON u."id" = p."authorId"
		WHERE p."communityId" = ANY($1) AND p."deletedAt" IS NULL
		ORDER BY p."createdAt" DESC, p."id" DESC
		LIMIT $2`, commIDs, candidateLimit)
}

func (s *Service) fetchPublicPosts(ctx context.Context, limit int) ([]rawPost, error) {
	return s.scanPosts(ctx, `
		SELECT p."id", p."authorId", u."displayName", u."avatarUrl", p."universityId", p."communityId",
		       p."content", p."visibility", p."likeCount", p."commentCount", p."deletedAt", p."createdAt", p."updatedAt"
		FROM "Post" p
		JOIN "User" u ON u."id" = p."authorId"
		WHERE p."visibility" = 'PUBLIC' AND p."deletedAt" IS NULL
		ORDER BY p."createdAt" DESC, p."id" DESC
		LIMIT $1`, limit)
}

func (s *Service) scanPosts(ctx context.Context, sql string, args ...any) ([]rawPost, error) {
	rows, err := s.db.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query posts: %w", err)
	}
	defer rows.Close()

	var list []rawPost
	for rows.Next() {
		var p rawPost
		if err := rows.Scan(
			&p.ID, &p.AuthorID, &p.AuthorName, &p.AuthorAvatar, &p.UniversityID, &p.CommunityID,
			&p.Content, &p.Visibility, &p.LikeCount, &p.CommentCount, &p.DeletedAt, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan post: %w", err)
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

func (s *Service) fetchEventsForUniversities(ctx context.Context, uniIDs []string) ([]rawEvent, error) {
	if len(uniIDs) == 0 {
		return nil, nil
	}
	return s.scanEvents(ctx, `
		SELECT e."id", e."organizerId", u."displayName", u."avatarUrl", e."universityId", e."communityId",
		       e."title", e."description", e."type", e."location", e."startsAt", e."endsAt",
		       e."capacity", e."registeredCount", e."registrationDeadline", e."visibility", e."status",
		       e."createdAt", e."updatedAt"
		FROM "Event" e
		JOIN "User" u ON u."id" = e."organizerId"
		WHERE e."universityId" = ANY($1) AND e."status" = 'PUBLISHED'
		ORDER BY e."createdAt" DESC, e."id" DESC
		LIMIT $2`, uniIDs, candidateLimit)
}

func (s *Service) fetchEventsForCommunities(ctx context.Context, commIDs []string) ([]rawEvent, error) {
	if len(commIDs) == 0 {
		return nil, nil
	}
	return s.scanEvents(ctx, `
		SELECT e."id", e."organizerId", u."displayName", u."avatarUrl", e."universityId", e."communityId",
		       e."title", e."description", e."type", e."location", e."startsAt", e."endsAt",
		       e."capacity", e."registeredCount", e."registrationDeadline", e."visibility", e."status",
		       e."createdAt", e."updatedAt"
		FROM "Event" e
		JOIN "User" u ON u."id" = e."organizerId"
		WHERE e."communityId" = ANY($1) AND e."status" = 'PUBLISHED'
		ORDER BY e."createdAt" DESC, e."id" DESC
		LIMIT $2`, commIDs, candidateLimit)
}

func (s *Service) fetchPublicEvents(ctx context.Context, limit int) ([]rawEvent, error) {
	return s.scanEvents(ctx, `
		SELECT e."id", e."organizerId", u."displayName", u."avatarUrl", e."universityId", e."communityId",
		       e."title", e."description", e."type", e."location", e."startsAt", e."endsAt",
		       e."capacity", e."registeredCount", e."registrationDeadline", e."visibility", e."status",
		       e."createdAt", e."updatedAt"
		FROM "Event" e
		JOIN "User" u ON u."id" = e."organizerId"
		WHERE e."visibility" = 'PUBLIC' AND e."status" = 'PUBLISHED'
		ORDER BY e."createdAt" DESC, e."id" DESC
		LIMIT $1`, limit)
}

func (s *Service) scanEvents(ctx context.Context, sql string, args ...any) ([]rawEvent, error) {
	rows, err := s.db.Pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var list []rawEvent
	for rows.Next() {
		var e rawEvent
		if err := rows.Scan(
			&e.ID, &e.OrganizerID, &e.OrganizerName, &e.OrganizerAvatar, &e.UniversityID, &e.CommunityID,
			&e.Title, &e.Description, &e.Type, &e.Location, &e.StartsAt, &e.EndsAt,
			&e.Capacity, &e.RegisteredCount, &e.RegistrationDeadline, &e.Visibility, &e.Status,
			&e.CreatedAt, &e.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		list = append(list, e)
	}
	return list, rows.Err()
}
