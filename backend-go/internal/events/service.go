package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/posts"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type eventRow struct {
	ID                   string
	OrganizerID          string
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
	OrganizerDisplayName *string
	OrganizerAvatarURL   *string
}

func (r eventRow) toView(myTicket *MyTicketSummary) EventView {
	available := r.Capacity - r.RegisteredCount
	if available < 0 {
		available = 0
	}
	return EventView{
		ID: r.ID,
		Organizer: EventOrganizer{
			ID:          r.OrganizerID,
			DisplayName: r.OrganizerDisplayName,
			AvatarURL:   r.OrganizerAvatarURL,
		},
		UniversityID:         r.UniversityID,
		CommunityID:          r.CommunityID,
		Title:                r.Title,
		Description:          r.Description,
		Type:                 r.Type,
		Location:             r.Location,
		StartsAt:             r.StartsAt,
		EndsAt:               r.EndsAt,
		Capacity:             r.Capacity,
		RegisteredCount:      r.RegisteredCount,
		AvailableSpots:       available,
		RegistrationDeadline: r.RegistrationDeadline,
		Visibility:           r.Visibility,
		Status:               r.Status,
		CreatedAt:            r.CreatedAt,
		UpdatedAt:            r.UpdatedAt,
		MyTicket:             myTicket,
	}
}

// Service manages events.
type Service struct {
	db *database.DB
}

// NewService constructs an event Service.
func NewService(db *database.DB) *Service {
	return &Service{db: db}
}

func assertDateCoherence(startsAt time.Time, endsAt, deadline *time.Time) error {
	if endsAt != nil && !endsAt.After(startsAt) {
		return httpx.ValidationMessage("endsAt must be after startsAt.")
	}
	if deadline != nil && deadline.After(startsAt) {
		return httpx.ValidationMessage("registrationDeadline cannot be after the event starts.")
	}
	return nil
}

func (s *Service) resolveCommunity(ctx context.Context, organizerID, universityID string, communityID *string) error {
	if communityID == nil {
		return nil
	}
	var commUnivID string
	var isActive bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "universityId", "isActive"
		FROM "Community"
		WHERE "id" = $1`, *communityID,
	).Scan(&commUnivID, &isActive)
	if errors.Is(err, pgx.ErrNoRows) || !isActive {
		return httpx.NewError(404, httpx.CodeNotFound, "Community not found.")
	}
	if err != nil {
		return fmt.Errorf("query community: %w", err)
	}
	if commUnivID != universityID {
		return httpx.Forbidden("Community must belong to the event university.")
	}

	var status string
	err = s.db.Pool.QueryRow(ctx, `
		SELECT "status"
		FROM "CommunityMembership"
		WHERE "userId" = $1 AND "communityId" = $2`, organizerID, *communityID,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) || status != "ACTIVE" {
		return httpx.Forbidden("Join the community before attaching events to it.")
	}
	if err != nil {
		return fmt.Errorf("check community membership: %w", err)
	}
	return nil
}

func (s *Service) canViewEvent(ctx context.Context, vctx posts.ViewerContext, row eventRow) (bool, error) {
	if row.OrganizerID == vctx.UserID {
		return true, nil
	}
	if row.Status == "DRAFT" {
		return false, nil
	}
	for _, b := range vctx.BlockedIDs {
		if b == row.OrganizerID {
			return false, nil
		}
	}
	if row.Status == "CANCELLED" {
		var exists bool
		err := s.db.Pool.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM "Ticket" WHERE "eventId" = $1 AND "userId" = $2)`,
			row.ID, vctx.UserID,
		).Scan(&exists)
		if err != nil {
			return false, fmt.Errorf("check ticket: %w", err)
		}
		return exists, nil
	}
	switch row.Visibility {
	case "PUBLIC":
		return true, nil
	case "UNIVERSITY":
		for _, u := range vctx.UniversityIDs {
			if u == row.UniversityID {
				return true, nil
			}
		}
		return false, nil
	case "COMMUNITY":
		if row.CommunityID == nil {
			return false, nil
		}
		for _, c := range vctx.CommunityIDs {
			if c == *row.CommunityID {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, nil
	}
}

func (s *Service) fetchEventRow(ctx context.Context, id string) (eventRow, error) {
	var r eventRow
	err := s.db.Pool.QueryRow(ctx, `
		SELECT e."id", e."organizerId", e."universityId", e."communityId", e."title", e."description",
		       e."type", e."location", e."startsAt", e."endsAt", e."capacity", e."registeredCount",
		       e."registrationDeadline", e."visibility", e."status", e."createdAt", e."updatedAt",
		       u."displayName", u."avatarUrl"
		FROM "Event" e
		JOIN "User" u ON u."id" = e."organizerId"
		WHERE e."id" = $1`, id,
	).Scan(
		&r.ID, &r.OrganizerID, &r.UniversityID, &r.CommunityID, &r.Title, &r.Description,
		&r.Type, &r.Location, &r.StartsAt, &r.EndsAt, &r.Capacity, &r.RegisteredCount,
		&r.RegistrationDeadline, &r.Visibility, &r.Status, &r.CreatedAt, &r.UpdatedAt,
		&r.OrganizerDisplayName, &r.OrganizerAvatarURL,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return eventRow{}, httpx.NewError(404, httpx.CodeNotFound, "Event not found.")
	}
	if err != nil {
		return eventRow{}, fmt.Errorf("fetch event: %w", err)
	}
	return r, nil
}

// Create inserts a new event in DRAFT status.
func (s *Service) Create(ctx context.Context, organizerID string, req CreateEventRequest) (EventView, error) {
	// Organizer must have an active membership in an active university.
	var universityID string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT m."universityId"
		FROM "UniversityMembership" m
		JOIN "University" u ON u."id" = m."universityId"
		WHERE m."userId" = $1 AND m."status" = 'ACTIVE' AND u."isActive" = true
		LIMIT 1`, organizerID,
	).Scan(&universityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return EventView{}, httpx.Forbidden("Join an active university before creating events.")
	}
	if err != nil {
		return EventView{}, fmt.Errorf("check university membership: %w", err)
	}

	if req.Visibility == "COMMUNITY" && req.CommunityID == nil {
		return EventView{}, httpx.ValidationMessage("Community events require a community.")
	}

	if err := s.resolveCommunity(ctx, organizerID, universityID, req.CommunityID); err != nil {
		return EventView{}, err
	}

	if err := assertDateCoherence(req.StartsAt, req.EndsAt, req.RegistrationDeadline); err != nil {
		return EventView{}, err
	}

	eventID := uuid.NewString()
	now := time.Now().UTC()

	var r eventRow
	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO "Event" (
			"id", "organizerId", "universityId", "communityId", "title", "description",
			"type", "location", "startsAt", "endsAt", "capacity", "registeredCount",
			"registrationDeadline", "visibility", "status", "createdAt", "updatedAt"
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, $12, $13, 'DRAFT', $14, $14)
		RETURNING "id", "organizerId", "universityId", "communityId", "title", "description",
		          "type", "location", "startsAt", "endsAt", "capacity", "registeredCount",
		          "registrationDeadline", "visibility", "status", "createdAt", "updatedAt"`,
		eventID, organizerID, universityID, req.CommunityID, req.Title, req.Description,
		req.Type, req.Location, req.StartsAt, req.EndsAt, req.Capacity,
		req.RegistrationDeadline, req.Visibility, now,
	).Scan(
		&r.ID, &r.OrganizerID, &r.UniversityID, &r.CommunityID, &r.Title, &r.Description,
		&r.Type, &r.Location, &r.StartsAt, &r.EndsAt, &r.Capacity, &r.RegisteredCount,
		&r.RegistrationDeadline, &r.Visibility, &r.Status, &r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return EventView{}, fmt.Errorf("insert event: %w", err)
	}

	_ = s.db.Pool.QueryRow(ctx, `
		SELECT "displayName", "avatarUrl" FROM "User" WHERE "id" = $1`, organizerID,
	).Scan(&r.OrganizerDisplayName, &r.OrganizerAvatarURL)

	return r.toView(nil), nil
}

// Detail fetches an event and enforces visibility rules.
func (s *Service) Detail(ctx context.Context, viewerID, eventID string) (EventView, error) {
	row, err := s.fetchEventRow(ctx, eventID)
	if err != nil {
		return EventView{}, err
	}

	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return EventView{}, fmt.Errorf("load viewer context: %w", err)
	}

	canView, err := s.canViewEvent(ctx, vctx, row)
	if err != nil {
		return EventView{}, err
	}
	if !canView {
		return EventView{}, httpx.NewError(404, httpx.CodeNotFound, "Event not found.")
	}

	var myTicket *MyTicketSummary
	var ticketID, ticketStatus string
	tErr := s.db.Pool.QueryRow(ctx, `
		SELECT "id", "status"
		FROM "Ticket"
		WHERE "eventId" = $1 AND "userId" = $2`, eventID, viewerID,
	).Scan(&ticketID, &ticketStatus)
	if tErr == nil {
		myTicket = &MyTicketSummary{ID: ticketID, Status: ticketStatus}
	}

	return row.toView(myTicket), nil
}

var validTransitions = map[string][]string{
	"DRAFT":     {"PUBLISHED", "CANCELLED"},
	"PUBLISHED": {"CANCELLED", "COMPLETED"},
	"CANCELLED": {},
	"COMPLETED": {},
}

// Update modifies an existing event.
func (s *Service) Update(ctx context.Context, viewerID, eventID string, req UpdateEventRequest) (EventView, error) {
	row, err := s.fetchEventRow(ctx, eventID)
	if err != nil {
		return EventView{}, err
	}

	if row.OrganizerID != viewerID {
		return EventView{}, httpx.Forbidden("Only the organizer can update this event.")
	}

	if row.Status == "CANCELLED" || row.Status == "COMPLETED" {
		return EventView{}, httpx.ValidationMessage("Closed events cannot be edited.")
	}

	startsAt := row.StartsAt
	if req.StartsAt != nil {
		startsAt = *req.StartsAt
	}
	endsAt := row.EndsAt
	if req.ClearEndsAt {
		endsAt = nil
	} else if req.EndsAt != nil {
		endsAt = req.EndsAt
	}
	deadline := row.RegistrationDeadline
	if req.ClearDeadline {
		deadline = nil
	} else if req.RegistrationDeadline != nil {
		deadline = req.RegistrationDeadline
	}

	if req.StartsAt != nil || req.EndsAt != nil || req.ClearEndsAt || req.RegistrationDeadline != nil || req.ClearDeadline {
		if err := assertDateCoherence(startsAt, endsAt, deadline); err != nil {
			return EventView{}, err
		}
	}

	if req.Capacity != nil {
		if *req.Capacity < row.RegisteredCount {
			return EventView{}, httpx.ValidationMessage("Capacity cannot drop below the registered count.")
		}
	}

	effectiveCommunity := row.CommunityID
	if req.ClearCommunity {
		effectiveCommunity = nil
	} else if req.CommunityID != nil {
		effectiveCommunity = req.CommunityID
		if err := s.resolveCommunity(ctx, viewerID, row.UniversityID, effectiveCommunity); err != nil {
			return EventView{}, err
		}
	}

	visibility := row.Visibility
	if req.Visibility != nil {
		visibility = *req.Visibility
		if visibility == "COMMUNITY" && effectiveCommunity == nil {
			return EventView{}, httpx.ValidationMessage("Community events require a community.")
		}
	} else if row.Visibility == "COMMUNITY" && req.ClearCommunity {
		return EventView{}, httpx.ValidationMessage("Detaching the community requires changing visibility first.")
	}

	status := row.Status
	if req.Status != nil && *req.Status != row.Status {
		allowed := validTransitions[row.Status]
		isAllowed := false
		for _, a := range allowed {
			if a == *req.Status {
				isAllowed = true
				break
			}
		}
		if !isAllowed {
			return EventView{}, httpx.ValidationMessage(fmt.Sprintf("Cannot transition event from %s to %s.", row.Status, *req.Status))
		}
		status = *req.Status
	}

	title := row.Title
	if req.Title != nil {
		title = *req.Title
	}
	desc := row.Description
	if req.ClearDescription {
		desc = nil
	} else if req.Description != nil {
		desc = req.Description
	}
	eType := row.Type
	if req.Type != nil {
		eType = *req.Type
	}
	loc := row.Location
	if req.ClearLocation {
		loc = nil
	} else if req.Location != nil {
		loc = req.Location
	}
	capacity := row.Capacity
	if req.Capacity != nil {
		capacity = *req.Capacity
	}

	now := time.Now().UTC()
	err = s.db.Pool.QueryRow(ctx, `
		UPDATE "Event"
		SET "title" = $1, "description" = $2, "type" = $3, "location" = $4,
		    "startsAt" = $5, "endsAt" = $6, "capacity" = $7, "registrationDeadline" = $8,
		    "communityId" = $9, "visibility" = $10, "status" = $11, "updatedAt" = $12
		WHERE "id" = $13
		RETURNING "id", "organizerId", "universityId", "communityId", "title", "description",
		          "type", "location", "startsAt", "endsAt", "capacity", "registeredCount",
		          "registrationDeadline", "visibility", "status", "createdAt", "updatedAt"`,
		title, desc, eType, loc, startsAt, endsAt, capacity, deadline,
		effectiveCommunity, visibility, status, now, eventID,
	).Scan(
		&row.ID, &row.OrganizerID, &row.UniversityID, &row.CommunityID, &row.Title, &row.Description,
		&row.Type, &row.Location, &row.StartsAt, &row.EndsAt, &row.Capacity, &row.RegisteredCount,
		&row.RegistrationDeadline, &row.Visibility, &row.Status, &row.CreatedAt, &row.UpdatedAt,
	)
	if err != nil {
		return EventView{}, fmt.Errorf("update event: %w", err)
	}

	return row.toView(nil), nil
}

// Cancel transitions an event to CANCELLED and creates an outbox event.
func (s *Service) Cancel(ctx context.Context, viewerID, eventID string) (EventView, error) {
	row, err := s.fetchEventRow(ctx, eventID)
	if err != nil {
		return EventView{}, err
	}

	if row.OrganizerID != viewerID {
		return EventView{}, httpx.Forbidden("Only the organizer can cancel this event.")
	}

	if row.Status == "CANCELLED" {
		return row.toView(nil), nil
	}
	if row.Status == "COMPLETED" {
		return EventView{}, httpx.ValidationMessage("Completed events cannot be cancelled.")
	}

	payload, err := json.Marshal(map[string]any{
		"eventId":     eventID,
		"organizerId": viewerID,
		"title":       row.Title,
	})
	if err != nil {
		return EventView{}, fmt.Errorf("marshal outbox payload: %w", err)
	}

	now := time.Now().UTC()
	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE "Event"
			SET "status" = 'CANCELLED', "updatedAt" = $1
			WHERE "id" = $2`, now, eventID)
		if err != nil {
			return fmt.Errorf("update event status: %w", err)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO "DomainEvent" (
				"id", "type", "aggregateType", "aggregateId", "actorId", "payload", "status", "attempts", "availableAt", "createdAt"
			) VALUES ($1, 'event.cancelled', 'Event', $2, $3, $4, 'PENDING', 0, $5, $5)`,
			uuid.NewString(), eventID, viewerID, payload, now)
		if err != nil {
			return fmt.Errorf("insert domain event: %w", err)
		}
		return nil
	})
	if err != nil {
		return EventView{}, err
	}

	row.Status = "CANCELLED"
	row.UpdatedAt = now
	return row.toView(nil), nil
}

// List discovers events with cursor pagination.
func (s *Service) List(ctx context.Context, viewerID string, query EventListQuery, page httpx.Page) (httpx.PageResult[EventView], error) {
	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return httpx.PageResult[EventView]{}, fmt.Errorf("load viewer context: %w", err)
	}

	if query.Mine {
		return s.listMine(ctx, viewerID, query, page)
	}

	status := "PUBLISHED"
	if query.Status != nil {
		status = *query.Status
	}
	if status == "DRAFT" || status == "CANCELLED" {
		return httpx.PageResult[EventView]{}, httpx.ValidationMessage("Drafts and cancelled events are only listed via mine=true.")
	}

	if query.UniversityID != nil {
		hasUni := false
		for _, u := range vctx.UniversityIDs {
			if u == *query.UniversityID {
				hasUni = true
				break
			}
		}
		if !hasUni {
			return httpx.PageResult[EventView]{}, httpx.Forbidden("Event discovery is scoped to your universities.")
		}
	}

	if query.CommunityID != nil {
		var commVisibility string
		var isActive bool
		err := s.db.Pool.QueryRow(ctx, `
			SELECT "visibility", "isActive" FROM "Community" WHERE "id" = $1`, *query.CommunityID,
		).Scan(&commVisibility, &isActive)
		if errors.Is(err, pgx.ErrNoRows) || !isActive {
			return httpx.PageResult[EventView]{}, httpx.NewError(404, httpx.CodeNotFound, "Community not found.")
		}
		if commVisibility == "PRIVATE" {
			hasComm := false
			for _, c := range vctx.CommunityIDs {
				if c == *query.CommunityID {
					hasComm = true
					break
				}
			}
			if !hasComm {
				return httpx.PageResult[EventView]{}, httpx.Forbidden("This community is private.")
			}
		}
	}

	return s.queryEvents(ctx, vctx, query, status, page)
}

func (s *Service) listMine(ctx context.Context, viewerID string, query EventListQuery, page httpx.Page) (httpx.PageResult[EventView], error) {
	baseQuery := `
		SELECT e."id", e."organizerId", e."universityId", e."communityId", e."title", e."description",
		       e."type", e."location", e."startsAt", e."endsAt", e."capacity", e."registeredCount",
		       e."registrationDeadline", e."visibility", e."status", e."createdAt", e."updatedAt",
		       u."displayName", u."avatarUrl"
		FROM "Event" e
		JOIN "User" u ON u."id" = e."organizerId"
		WHERE e."organizerId" = $1`
	args := []any{viewerID}

	if query.Status != nil {
		args = append(args, *query.Status)
		baseQuery += fmt.Sprintf(` AND e."status" = $%d`, len(args))
	}
	if query.Upcoming {
		baseQuery += ` AND e."startsAt" > NOW()`
	}

	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		baseQuery += fmt.Sprintf(` AND (e."startsAt" > $%d OR (e."startsAt" = $%d AND e."id" > $%d))`,
			len(args)-1, len(args)-1, len(args))
	}

	args = append(args, page.Limit+1)
	baseQuery += fmt.Sprintf(` ORDER BY e."startsAt" ASC, e."id" ASC LIMIT $%d`, len(args))

	return s.scanAndPage(ctx, baseQuery, args, page.Limit)
}

func (s *Service) queryEvents(ctx context.Context, vctx posts.ViewerContext, query EventListQuery, status string, page httpx.Page) (httpx.PageResult[EventView], error) {
	baseQuery := `
		SELECT e."id", e."organizerId", e."universityId", e."communityId", e."title", e."description",
		       e."type", e."location", e."startsAt", e."endsAt", e."capacity", e."registeredCount",
		       e."registrationDeadline", e."visibility", e."status", e."createdAt", e."updatedAt",
		       u."displayName", u."avatarUrl"
		FROM "Event" e
		JOIN "User" u ON u."id" = e."organizerId"
		WHERE e."status" = $1`
	args := []any{status}

	if len(vctx.BlockedIDs) > 0 {
		args = append(args, vctx.BlockedIDs)
		baseQuery += fmt.Sprintf(` AND NOT (e."organizerId" = ANY($%d))`, len(args))
	}

	visClauses := []string{`e."visibility" = 'PUBLIC'`}
	if len(vctx.UniversityIDs) > 0 {
		args = append(args, vctx.UniversityIDs)
		visClauses = append(visClauses, fmt.Sprintf(`(e."visibility" = 'UNIVERSITY' AND e."universityId" = ANY($%d))`, len(args)))
	}
	if len(vctx.CommunityIDs) > 0 {
		args = append(args, vctx.CommunityIDs)
		visClauses = append(visClauses, fmt.Sprintf(`(e."visibility" = 'COMMUNITY' AND e."communityId" = ANY($%d))`, len(args)))
	}
	baseQuery += " AND (" + visClauses[0]
	for i := 1; i < len(visClauses); i++ {
		baseQuery += " OR " + visClauses[i]
	}
	baseQuery += ")"

	if query.UniversityID != nil {
		args = append(args, *query.UniversityID)
		baseQuery += fmt.Sprintf(` AND e."universityId" = $%d`, len(args))
	}
	if query.CommunityID != nil {
		args = append(args, *query.CommunityID)
		baseQuery += fmt.Sprintf(` AND e."communityId" = $%d`, len(args))
	}
	if query.Upcoming {
		baseQuery += ` AND e."startsAt" > NOW()`
	}

	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		baseQuery += fmt.Sprintf(` AND (e."startsAt" > $%d OR (e."startsAt" = $%d AND e."id" > $%d))`,
			len(args)-1, len(args)-1, len(args))
	}

	args = append(args, page.Limit+1)
	baseQuery += fmt.Sprintf(` ORDER BY e."startsAt" ASC, e."id" ASC LIMIT $%d`, len(args))

	return s.scanAndPage(ctx, baseQuery, args, page.Limit)
}

func (s *Service) scanAndPage(ctx context.Context, sql string, args []any, limit int) (httpx.PageResult[EventView], error) {
	rows, err := s.db.Pool.Query(ctx, sql, args...)
	if err != nil {
		return httpx.PageResult[EventView]{}, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var list []eventRow
	for rows.Next() {
		var r eventRow
		if err := rows.Scan(
			&r.ID, &r.OrganizerID, &r.UniversityID, &r.CommunityID, &r.Title, &r.Description,
			&r.Type, &r.Location, &r.StartsAt, &r.EndsAt, &r.Capacity, &r.RegisteredCount,
			&r.RegistrationDeadline, &r.Visibility, &r.Status, &r.CreatedAt, &r.UpdatedAt,
			&r.OrganizerDisplayName, &r.OrganizerAvatarURL,
		); err != nil {
			return httpx.PageResult[EventView]{}, fmt.Errorf("scan event: %w", err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[EventView]{}, err
	}

	var items []EventView
	hasMore := len(list) > limit
	take := len(list)
	if hasMore {
		take = limit
	}
	for i := 0; i < take; i++ {
		items = append(items, list[i].toView(nil))
	}

	var nextCursor *string
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		cursor := httpx.EncodeCursor(last.StartsAt, last.ID)
		nextCursor = &cursor
	}

	return httpx.PageResult[EventView]{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

// ListByUniversity lists events for a university by slug.
func (s *Service) ListByUniversity(ctx context.Context, viewerID, slug string, page httpx.Page) (httpx.PageResult[EventView], error) {
	var univID string
	var isActive bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "id", "isActive" FROM "University" WHERE "slug" = $1`, slug,
	).Scan(&univID, &isActive)
	if errors.Is(err, pgx.ErrNoRows) || !isActive {
		return httpx.PageResult[EventView]{}, httpx.NewError(404, httpx.CodeNotFound, "University not found.")
	}
	if err != nil {
		return httpx.PageResult[EventView]{}, fmt.Errorf("find university: %w", err)
	}

	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return httpx.PageResult[EventView]{}, fmt.Errorf("load viewer context: %w", err)
	}

	hasMembership := false
	for _, u := range vctx.UniversityIDs {
		if u == univID {
			hasMembership = true
			break
		}
	}
	if !hasMembership {
		return httpx.PageResult[EventView]{}, httpx.Forbidden("University events are restricted to its members.")
	}

	query := EventListQuery{UniversityID: &univID}
	return s.queryEvents(ctx, vctx, query, "PUBLISHED", page)
}

// ListByCommunity lists events for a community by ID.
func (s *Service) ListByCommunity(ctx context.Context, viewerID, communityID string, page httpx.Page) (httpx.PageResult[EventView], error) {
	var commVisibility string
	var isActive bool
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "visibility", "isActive" FROM "Community" WHERE "id" = $1`, communityID,
	).Scan(&commVisibility, &isActive)
	if errors.Is(err, pgx.ErrNoRows) || !isActive {
		return httpx.PageResult[EventView]{}, httpx.NewError(404, httpx.CodeNotFound, "Community not found.")
	}
	if err != nil {
		return httpx.PageResult[EventView]{}, fmt.Errorf("find community: %w", err)
	}

	vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
	if err != nil {
		return httpx.PageResult[EventView]{}, fmt.Errorf("load viewer context: %w", err)
	}

	if commVisibility == "PRIVATE" {
		hasComm := false
		for _, c := range vctx.CommunityIDs {
			if c == communityID {
				hasComm = true
				break
			}
		}
		if !hasComm {
			return httpx.PageResult[EventView]{}, httpx.Forbidden("This community is private.")
		}
	}

	query := EventListQuery{CommunityID: &communityID}
	return s.queryEvents(ctx, vctx, query, "PUBLISHED", page)
}
