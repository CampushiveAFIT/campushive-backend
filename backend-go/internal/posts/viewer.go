package posts

import (
	"context"
	"fmt"
	"time"

	"campus-hive/backend-go/internal/database"
)

// ViewerContext is loaded once per request and reused for all visibility decisions.
type ViewerContext struct {
	UserID        string
	UniversityIDs []string
	CommunityIDs  []string
	FollowingIDs  []string
	BlockedIDs    []string
}

// PostAccess is the minimal shape needed for CanViewPost.
type PostAccess struct {
	AuthorID     string
	UniversityID string
	CommunityID  *string
	Visibility   string
	DeletedAt    *time.Time
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// LoadViewerContext loads all membership/follow/block data for a viewer in one
// parallel batch of five queries.
func LoadViewerContext(ctx context.Context, db *database.DB, userID string) (ViewerContext, error) {
	type result struct {
		ids []string
		err error
	}

	fetch := func(query string, args ...any) <-chan result {
		ch := make(chan result, 1)
		go func() {
			rows, err := db.Pool.Query(ctx, query, args...)
			if err != nil {
				ch <- result{err: fmt.Errorf("query: %w", err)}
				return
			}
			defer rows.Close()
			var ids []string
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					ch <- result{err: fmt.Errorf("scan: %w", err)}
					return
				}
				ids = append(ids, id)
			}
			ch <- result{ids: ids, err: rows.Err()}
		}()
		return ch
	}

	uniCh := fetch(`SELECT "universityId" FROM "UniversityMembership" WHERE "userId" = $1 AND "status" = 'ACTIVE'`, userID)
	commCh := fetch(`SELECT "communityId" FROM "CommunityMembership" WHERE "userId" = $1 AND "status" = 'ACTIVE'`, userID)
	followCh := fetch(`SELECT "followingId" FROM "Follow" WHERE "followerId" = $1`, userID)
	givenCh := fetch(`SELECT "blockedId" FROM "Block" WHERE "blockerId" = $1`, userID)
	receivedCh := fetch(`SELECT "blockerId" FROM "Block" WHERE "blockedId" = $1`, userID)

	uni := <-uniCh
	if uni.err != nil {
		return ViewerContext{}, fmt.Errorf("load university ids: %w", uni.err)
	}
	comm := <-commCh
	if comm.err != nil {
		return ViewerContext{}, fmt.Errorf("load community ids: %w", comm.err)
	}
	follow := <-followCh
	if follow.err != nil {
		return ViewerContext{}, fmt.Errorf("load following ids: %w", follow.err)
	}
	given := <-givenCh
	if given.err != nil {
		return ViewerContext{}, fmt.Errorf("load blocked ids: %w", given.err)
	}
	received := <-receivedCh
	if received.err != nil {
		return ViewerContext{}, fmt.Errorf("load blocker ids: %w", received.err)
	}

	// Merge both block directions.
	blockedSet := make(map[string]bool)
	for _, id := range given.ids {
		blockedSet[id] = true
	}
	for _, id := range received.ids {
		blockedSet[id] = true
	}
	blocked := make([]string, 0, len(blockedSet))
	for id := range blockedSet {
		blocked = append(blocked, id)
	}

	return ViewerContext{
		UserID:        userID,
		UniversityIDs: uni.ids,
		CommunityIDs:  comm.ids,
		FollowingIDs:  follow.ids,
		BlockedIDs:    blocked,
	}, nil
}

// CanViewPost applies the visibility rules from the NestJS reference.
func CanViewPost(ctx ViewerContext, p PostAccess) bool {
	if p.DeletedAt != nil {
		return false
	}
	if p.AuthorID == ctx.UserID {
		return true
	}
	if contains(ctx.BlockedIDs, p.AuthorID) {
		return false
	}
	switch p.Visibility {
	case "PUBLIC":
		return true
	case "UNIVERSITY":
		return contains(ctx.UniversityIDs, p.UniversityID)
	case "COMMUNITY":
		if p.CommunityID == nil {
			return false
		}
		return contains(ctx.CommunityIDs, *p.CommunityID)
	case "FOLLOWERS":
		return contains(ctx.FollowingIDs, p.AuthorID)
	default:
		return false
	}
}

// EventSummaryShape is the slice of an Event row that the post response embeds.
type EventSummaryShape struct {
	OrganizerID  string
	UniversityID string
	CommunityID  *string
	Title        string
	StartsAt     time.Time
	EndsAt       *time.Time
	Location     *string
	Status       string
	Visibility   string
}

// EventSummaryVisible decides whether a referenced event's summary may be
// embedded in a post response. Organizers always see their events; drafts are
// organizer-only; otherwise the event's own visibility rules plus block rules
// apply. It is a pure function of the already-loaded viewer context.
func EventSummaryVisible(vctx ViewerContext, event EventSummaryShape) bool {
	if event.OrganizerID == vctx.UserID {
		return true
	}
	if event.Status == "DRAFT" {
		return false
	}
	if contains(vctx.BlockedIDs, event.OrganizerID) {
		return false
	}
	switch event.Visibility {
	case "PUBLIC":
		return true
	case "UNIVERSITY":
		return contains(vctx.UniversityIDs, event.UniversityID)
	case "COMMUNITY":
		return event.CommunityID != nil && contains(vctx.CommunityIDs, *event.CommunityID)
	default:
		return false
	}
}
