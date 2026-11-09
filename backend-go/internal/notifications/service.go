package notifications

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"

	"github.com/jackc/pgx/v5"
)

// Service handles notification business logic.
type Service struct {
	db  *database.DB
	now func() time.Time
}

// NewService constructs a Service.
func NewService(db *database.DB) *Service {
	return &Service{db: db, now: time.Now}
}

// defaultPrefs returns the default preference view (no row in DB yet).
var defaultPrefs = PreferenceView{
	LikesEnabled:        true,
	CommentsEnabled:     true,
	FollowsEnabled:      true,
	EventUpdatesEnabled: true,
	UpdatedAt:           nil,
}

// notificationColumns is the SELECT list used when fetching notifications with
// an optional actor join. The actor columns are nullable via LEFT JOIN.
const notificationColumns = `n."id",n."type",n."title",n."body",n."data",n."isRead",n."readAt",n."createdAt",` +
	`u."id",u."displayName",u."avatarUrl"`

func scanNotification(row interface{ Scan(...any) error }) (notificationRow, error) {
	var nr notificationRow
	err := row.Scan(
		&nr.ID, &nr.Type, &nr.Title, &nr.Body, &nr.Data, &nr.IsRead, &nr.ReadAt, &nr.CreatedAt,
		&nr.ActorID, &nr.ActorName, &nr.ActorAvatar,
	)
	return nr, err
}

// List returns a cursor-paginated list of notifications for viewerID.
func (s *Service) List(ctx context.Context, viewerID string, page httpx.Page, unreadOnly bool) (httpx.PageResult[NotificationView], error) {
	args := []any{viewerID}

	unreadWhere := ""
	if unreadOnly {
		unreadWhere = ` AND n."isRead" = false`
	}

	cursorWhere := ""
	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		n := len(args)
		cursorWhere = fmt.Sprintf(` AND (n."createdAt" < $%d OR (n."createdAt" = $%d AND n."id" < $%d))`, n-1, n-1, n)
	}

	args = append(args, page.Limit+1)
	limitN := fmt.Sprintf("$%d", len(args))

	query := `SELECT ` + notificationColumns + `
		FROM "Notification" n
		LEFT JOIN "User" u ON u."id" = n."actorId"
		WHERE n."recipientId" = $1` + unreadWhere + cursorWhere +
		` ORDER BY n."createdAt" DESC, n."id" DESC LIMIT ` + limitN

	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return httpx.PageResult[NotificationView]{}, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	var nrows []notificationRow
	for rows.Next() {
		nr, err := scanNotification(rows)
		if err != nil {
			return httpx.PageResult[NotificationView]{}, fmt.Errorf("scan notification: %w", err)
		}
		nrows = append(nrows, nr)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[NotificationView]{}, fmt.Errorf("iterate notifications: %w", err)
	}

	limit := page.Limit
	hasMore := len(nrows) > limit
	if hasMore {
		nrows = nrows[:limit]
	}

	items := make([]NotificationView, 0, len(nrows))
	for _, nr := range nrows {
		items = append(items, toNotificationView(nr))
	}

	result := httpx.PageResult[NotificationView]{Items: items}
	if hasMore && len(nrows) > 0 {
		last := nrows[len(nrows)-1]
		cursor := httpx.EncodeCursor(last.CreatedAt, last.ID)
		result.NextCursor = &cursor
	}
	return result, nil
}

// UnreadCount returns the number of unread notifications for viewerID.
func (s *Service) UnreadCount(ctx context.Context, viewerID string) (map[string]int, error) {
	var count int
	err := s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM "Notification" WHERE "recipientId" = $1 AND "isRead" = false`,
		viewerID).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("unread count: %w", err)
	}
	return map[string]int{"unread": count}, nil
}

// MarkRead marks a single notification as read. Returns 404 if not found or
// not owned by viewerID. Idempotent if already read.
func (s *Service) MarkRead(ctx context.Context, viewerID, id string) (NotificationView, error) {
	// Fetch the notification with actor join plus recipientId in one query.
	const markReadCols = `n."id",n."type",n."title",n."body",n."data",n."isRead",n."readAt",n."createdAt",` +
		`u."id",u."displayName",u."avatarUrl",n."recipientId"`

	var nr notificationRow
	var recipientID string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT `+markReadCols+`
		FROM "Notification" n
		LEFT JOIN "User" u ON u."id" = n."actorId"
		WHERE n."id" = $1`, id).Scan(
		&nr.ID, &nr.Type, &nr.Title, &nr.Body, &nr.Data, &nr.IsRead, &nr.ReadAt, &nr.CreatedAt,
		&nr.ActorID, &nr.ActorName, &nr.ActorAvatar, &recipientID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return NotificationView{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "Notification not found.")
		}
		return NotificationView{}, fmt.Errorf("get notification: %w", err)
	}

	// Check ownership (no-leak: 404 either way).
	if recipientID != viewerID {
		return NotificationView{}, httpx.NewError(http.StatusNotFound, httpx.CodeNotFound, "Notification not found.")
	}

	// Idempotent: already read.
	if nr.IsRead {
		return toNotificationView(nr), nil
	}

	// Mark as read then re-fetch with actor join.
	now := s.now()
	if _, err := s.db.Pool.Exec(ctx,
		`UPDATE "Notification" SET "isRead" = true, "readAt" = $1 WHERE "id" = $2`,
		now, id); err != nil {
		return NotificationView{}, fmt.Errorf("mark read: %w", err)
	}

	updRow := s.db.Pool.QueryRow(ctx,
		`SELECT `+notificationColumns+`
		FROM "Notification" n
		LEFT JOIN "User" u ON u."id" = n."actorId"
		WHERE n."id" = $1`, id)
	nr, err = scanNotification(updRow)
	if err != nil {
		return NotificationView{}, fmt.Errorf("fetch updated notification: %w", err)
	}
	return toNotificationView(nr), nil
}

// MarkAllRead marks all unread notifications as read for viewerID.
func (s *Service) MarkAllRead(ctx context.Context, viewerID string) (map[string]int64, error) {
	now := s.now()
	tag, err := s.db.Pool.Exec(ctx,
		`UPDATE "Notification" SET "isRead" = true, "readAt" = $1 WHERE "recipientId" = $2 AND "isRead" = false`,
		now, viewerID)
	if err != nil {
		return nil, fmt.Errorf("mark all read: %w", err)
	}
	return map[string]int64{"marked": tag.RowsAffected()}, nil
}

// GetPreferences returns notification preferences for viewerID, returning defaults if not set.
func (s *Service) GetPreferences(ctx context.Context, viewerID string) (PreferenceView, error) {
	var pv PreferenceView
	err := s.db.Pool.QueryRow(ctx,
		`SELECT "likesEnabled","commentsEnabled","followsEnabled","eventUpdatesEnabled","updatedAt"
		FROM "NotificationPreference" WHERE "userId" = $1`, viewerID).Scan(
		&pv.LikesEnabled, &pv.CommentsEnabled, &pv.FollowsEnabled, &pv.EventUpdatesEnabled, &pv.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return defaultPrefs, nil
		}
		return PreferenceView{}, fmt.Errorf("get preferences: %w", err)
	}
	return pv, nil
}

// UpdatePreferencesRequest holds the optional boolean fields the caller may set.
type UpdatePreferencesRequest struct {
	LikesEnabled        *bool
	CommentsEnabled     *bool
	FollowsEnabled      *bool
	EventUpdatesEnabled *bool
}

// UpdatePreferences upserts notification preferences for viewerID.
func (s *Service) UpdatePreferences(ctx context.Context, viewerID string, req UpdatePreferencesRequest) (PreferenceView, error) {
	now := s.now()

	// Build dynamic SET clause for the update branch of the upsert.
	setClauses := []string{`"updatedAt" = $2`}
	args := []any{viewerID, now}

	addField := func(col string, val *bool) {
		if val != nil {
			args = append(args, *val)
			setClauses = append(setClauses, fmt.Sprintf(`%q = $%d`, col, len(args)))
		}
	}
	addField("likesEnabled", req.LikesEnabled)
	addField("commentsEnabled", req.CommentsEnabled)
	addField("followsEnabled", req.FollowsEnabled)
	addField("eventUpdatesEnabled", req.EventUpdatesEnabled)

	// Build the INSERT columns/values.
	insertCols := `"userId","createdAt","updatedAt"`
	insertVals := `$1,$2,$2`
	if req.LikesEnabled != nil {
		args = append(args, *req.LikesEnabled)
		insertCols += `,"likesEnabled"`
		insertVals += fmt.Sprintf(",$%d", len(args))
	}
	if req.CommentsEnabled != nil {
		args = append(args, *req.CommentsEnabled)
		insertCols += `,"commentsEnabled"`
		insertVals += fmt.Sprintf(",$%d", len(args))
	}
	if req.FollowsEnabled != nil {
		args = append(args, *req.FollowsEnabled)
		insertCols += `,"followsEnabled"`
		insertVals += fmt.Sprintf(",$%d", len(args))
	}
	if req.EventUpdatesEnabled != nil {
		args = append(args, *req.EventUpdatesEnabled)
		insertCols += `,"eventUpdatesEnabled"`
		insertVals += fmt.Sprintf(",$%d", len(args))
	}

	setClause := ""
	for i, c := range setClauses {
		if i > 0 {
			setClause += ", "
		}
		setClause += c
	}

	query := fmt.Sprintf(
		`INSERT INTO "NotificationPreference" (%s) VALUES (%s)
		ON CONFLICT ("userId") DO UPDATE SET %s
		RETURNING "likesEnabled","commentsEnabled","followsEnabled","eventUpdatesEnabled","updatedAt"`,
		insertCols, insertVals, setClause)

	var pv PreferenceView
	err := s.db.Pool.QueryRow(ctx, query, args...).Scan(
		&pv.LikesEnabled, &pv.CommentsEnabled, &pv.FollowsEnabled, &pv.EventUpdatesEnabled, &pv.UpdatedAt)
	if err != nil {
		return PreferenceView{}, fmt.Errorf("upsert preferences: %w", err)
	}
	return pv, nil
}
