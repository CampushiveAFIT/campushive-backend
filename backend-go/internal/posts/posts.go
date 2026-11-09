package posts

import "time"

// PostAuthor is the safe user subset embedded in PostView.
type PostAuthor struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// EventSummaryView is the embedded reference of a post's event. Only the
// identity and scheduling fields are exposed; no event payload is copied.
type EventSummaryView struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	StartsAt   time.Time  `json:"startsAt"`
	EndsAt     *time.Time `json:"endsAt"`
	Location   *string    `json:"location"`
	Status     string     `json:"status"`
	Visibility string     `json:"visibility"`
}

// PostView is the public projection of a Post row.
type PostView struct {
	ID           string            `json:"id"`
	Author       PostAuthor        `json:"author"`
	UniversityID string            `json:"universityId"`
	CommunityID  *string           `json:"communityId"`
	Content      string            `json:"content"`
	Visibility   string            `json:"visibility"`
	LikeCount    int               `json:"likeCount"`
	CommentCount int               `json:"commentCount"`
	LikedByMe    bool              `json:"likedByMe"`
	Event        *EventSummaryView `json:"event"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

// CommentView is the public projection of a Comment row. It mirrors the
// reference, which spreads the Prisma record and therefore includes the
// authorId and the (always null) deletedAt scalar.
type CommentView struct {
	ID        string     `json:"id"`
	PostID    string     `json:"postId"`
	AuthorID  string     `json:"authorId"`
	Author    PostAuthor `json:"author"`
	Content   string     `json:"content"`
	DeletedAt *time.Time `json:"deletedAt"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// postRow is the internal row representation.
type postRow struct {
	ID           string
	AuthorID     string
	UniversityID string
	CommunityID  *string
	Content      string
	Visibility   string
	LikeCount    int
	CommentCount int
	DeletedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// Joined author fields
	AuthorDisplayName *string
	AuthorAvatarURL   *string
	// Joined event summary, NULL when the post references no event.
	EventID           *string
	EventOrganizerID  *string
	EventUniversityID *string
	EventCommunityID  *string
	EventTitle        *string
	EventStartsAt     *time.Time
	EventEndsAt       *time.Time
	EventLocation     *string
	EventStatus       *string
	EventVisibility   *string
}

// postColumns lists the Post row plus its joined author and event summary.
// The caller must join "User" u and left-join "Event" e.
const postColumns = `p."id",p."authorId",p."universityId",p."communityId",p."content",p."visibility",p."likeCount",p."commentCount",p."deletedAt",p."createdAt",p."updatedAt",` +
	`u."displayName",u."avatarUrl",` +
	`e."id",e."organizerId",e."universityId",e."communityId",e."title",e."startsAt",e."endsAt",e."location",e."status",e."visibility"`

const postJoins = `FROM "Post" p
		JOIN "User" u ON u."id" = p."authorId"
		LEFT JOIN "Event" e ON e."id" = p."eventId"`

// scanPostRow reads one full postColumns row.
func scanPostRow(row interface{ Scan(...any) error }) (postRow, error) {
	var p postRow
	err := row.Scan(
		&p.ID, &p.AuthorID, &p.UniversityID, &p.CommunityID, &p.Content,
		&p.Visibility, &p.LikeCount, &p.CommentCount, &p.DeletedAt, &p.CreatedAt, &p.UpdatedAt,
		&p.AuthorDisplayName, &p.AuthorAvatarURL,
		&p.EventID, &p.EventOrganizerID, &p.EventUniversityID, &p.EventCommunityID, &p.EventTitle,
		&p.EventStartsAt, &p.EventEndsAt, &p.EventLocation, &p.EventStatus, &p.EventVisibility,
	)
	return p, err
}

func toPostView(p postRow, likedByMe bool, vctx ViewerContext) PostView {
	return PostView{
		ID: p.ID,
		Author: PostAuthor{
			ID:          p.AuthorID,
			DisplayName: p.AuthorDisplayName,
			AvatarURL:   p.AuthorAvatarURL,
		},
		UniversityID: p.UniversityID,
		CommunityID:  p.CommunityID,
		Content:      p.Content,
		Visibility:   p.Visibility,
		LikeCount:    p.LikeCount,
		CommentCount: p.CommentCount,
		LikedByMe:    likedByMe,
		Event:        eventSummaryOf(p, vctx),
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

// eventSummaryOf renders the referenced event only when the viewer is allowed
// to see it; an event the viewer may not see collapses to null.
func eventSummaryOf(p postRow, vctx ViewerContext) *EventSummaryView {
	if p.EventID == nil || p.EventOrganizerID == nil || p.EventUniversityID == nil ||
		p.EventTitle == nil || p.EventStartsAt == nil || p.EventStatus == nil || p.EventVisibility == nil {
		return nil
	}
	if !EventSummaryVisible(vctx, EventSummaryShape{
		OrganizerID:  *p.EventOrganizerID,
		UniversityID: *p.EventUniversityID,
		CommunityID:  p.EventCommunityID,
		Title:        *p.EventTitle,
		StartsAt:     *p.EventStartsAt,
		EndsAt:       p.EventEndsAt,
		Location:     p.EventLocation,
		Status:       *p.EventStatus,
		Visibility:   *p.EventVisibility,
	}) {
		return nil
	}
	return &EventSummaryView{
		ID:         *p.EventID,
		Title:      *p.EventTitle,
		StartsAt:   *p.EventStartsAt,
		EndsAt:     p.EventEndsAt,
		Location:   p.EventLocation,
		Status:     *p.EventStatus,
		Visibility: *p.EventVisibility,
	}
}

// commentRow is the internal comment row.
type commentRow struct {
	ID          string
	PostID      string
	AuthorID    string
	Content     string
	DeletedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DisplayName *string
	AvatarURL   *string
}
