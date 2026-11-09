package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/posts"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// TicketsService handles registration, check-in and attendee ticket management.
type TicketsService struct {
	db     *database.DB
	events *Service
}

// NewTicketsService constructs a TicketsService.
func NewTicketsService(db *database.DB, events *Service) *TicketsService {
	return &TicketsService{db: db, events: events}
}

func (s *TicketsService) fetchTicketView(ctx context.Context, ticketID string) (TicketView, error) {
	var t TicketView
	err := s.db.Pool.QueryRow(ctx, `
		SELECT t."id", t."ticketCode", t."status", t."registeredAt", t."cancelledAt", t."checkedInAt", t."createdAt",
		       e."id", e."title", e."startsAt", e."endsAt", e."location", e."status",
		       u."id", u."displayName"
		FROM "Ticket" t
		JOIN "Event" e ON e."id" = t."eventId"
		JOIN "User" u ON u."id" = t."userId"
		WHERE t."id" = $1`, ticketID,
	).Scan(
		&t.ID, &t.TicketCode, &t.Status, &t.RegisteredAt, &t.CancelledAt, &t.CheckedInAt, &t.CreatedAt,
		&t.Event.ID, &t.Event.Title, &t.Event.StartsAt, &t.Event.EndsAt, &t.Event.Location, &t.Event.Status,
		&t.Attendee.ID, &t.Attendee.DisplayName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TicketView{}, httpx.NewError(404, httpx.CodeNotFound, "Ticket not found.")
	}
	if err != nil {
		return TicketView{}, fmt.Errorf("fetch ticket view: %w", err)
	}
	return t, nil
}

// Register reserves a ticket for the viewer in an event.
func (s *TicketsService) Register(ctx context.Context, viewerID, eventID string) (TicketView, bool, error) {
	row, err := s.events.fetchEventRow(ctx, eventID)
	if err != nil {
		return TicketView{}, false, err
	}

	if row.OrganizerID != viewerID {
		vctx, err := posts.LoadViewerContext(ctx, s.db, viewerID)
		if err != nil {
			return TicketView{}, false, fmt.Errorf("load viewer context: %w", err)
		}

		for _, b := range vctx.BlockedIDs {
			if b == row.OrganizerID {
				return TicketView{}, false, httpx.Forbidden("You cannot register for this event.")
			}
		}

		canView, err := s.events.canViewEvent(ctx, vctx, row)
		if err != nil {
			return TicketView{}, false, err
		}
		if !canView {
			if row.Status != "PUBLISHED" {
				return TicketView{}, false, httpx.Conflict(httpx.CodeRegistrationClosed, "Registration is not open for this event.")
			}
			return TicketView{}, false, httpx.NewError(404, httpx.CodeNotFound, "Event not found.")
		}
	}

	if row.Status != "PUBLISHED" {
		msg := "Registration is closed for this event."
		if row.Status == "DRAFT" {
			msg = "Registration is not open yet."
		}
		return TicketView{}, false, httpx.Conflict(httpx.CodeRegistrationClosed, msg)
	}
	if row.RegistrationDeadline != nil && !row.RegistrationDeadline.After(time.Now().UTC()) {
		return TicketView{}, false, httpx.Conflict(httpx.CodeRegistrationClosed, "The registration deadline has passed.")
	}

	var ticketID string
	var created bool

	err = s.db.InTx(ctx, func(tx pgx.Tx) error {
		var status string
		var registeredCount, capacity int
		var deadline *time.Time
		err := tx.QueryRow(ctx, `
			SELECT "status", "registeredCount", "capacity", "registrationDeadline"
			FROM "Event"
			WHERE "id" = $1
			FOR UPDATE`, eventID,
		).Scan(&status, &registeredCount, &capacity, &deadline)
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(404, httpx.CodeNotFound, "Event not found.")
		}
		if err != nil {
			return fmt.Errorf("lock event: %w", err)
		}

		if status != "PUBLISHED" {
			return httpx.Conflict(httpx.CodeRegistrationClosed, "Registration is not open for this event.")
		}
		if deadline != nil && !deadline.After(time.Now().UTC()) {
			return httpx.Conflict(httpx.CodeRegistrationClosed, "The registration deadline has passed.")
		}

		var existingID, existingStatus string
		err = tx.QueryRow(ctx, `
			SELECT "id", "status"
			FROM "Ticket"
			WHERE "eventId" = $1 AND "userId" = $2`, eventID, viewerID,
		).Scan(&existingID, &existingStatus)

		if err == nil {
			if existingStatus == "ACTIVE" || existingStatus == "CHECKED_IN" {
				ticketID = existingID
				created = false
				return nil
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("query existing ticket: %w", err)
		}

		if registeredCount >= capacity {
			return httpx.Conflict(httpx.CodeEventFull, "This event is full.")
		}

		now := time.Now().UTC()
		if existingID != "" {
			_, err = tx.Exec(ctx, `
				UPDATE "Ticket"
				SET "status" = 'ACTIVE', "cancelledAt" = NULL, "registeredAt" = $1
				WHERE "id" = $2`, now, existingID)
			if err != nil {
				return fmt.Errorf("reactivate ticket: %w", err)
			}
			ticketID = existingID
			created = false
		} else {
			code, err := newTicketCode()
			if err != nil {
				return fmt.Errorf("generate ticket code: %w", err)
			}
			ticketID = uuid.NewString()
			_, err = tx.Exec(ctx, `
				INSERT INTO "Ticket" (
					"id", "eventId", "userId", "ticketCode", "status", "registeredAt", "createdAt"
				) VALUES ($1, $2, $3, $4, 'ACTIVE', $5, $5)`,
				ticketID, eventID, viewerID, code, now)
			if err != nil {
				if database.IsUniqueViolation(err) {
					var winnerID string
					if errWinner := tx.QueryRow(ctx, `
						SELECT "id" FROM "Ticket" WHERE "eventId" = $1 AND "userId" = $2`, eventID, viewerID,
					).Scan(&winnerID); errWinner == nil {
						ticketID = winnerID
						created = false
						return nil
					}
				}
				return fmt.Errorf("insert ticket: %w", err)
			}
			created = true
		}

		_, err = tx.Exec(ctx, `
			UPDATE "Event"
			SET "registeredCount" = "registeredCount" + 1
			WHERE "id" = $1`, eventID)
		if err != nil {
			return fmt.Errorf("increment registered count: %w", err)
		}
		return nil
	})
	if err != nil {
		return TicketView{}, false, err
	}

	view, err := s.fetchTicketView(ctx, ticketID)
	if err != nil {
		return TicketView{}, false, err
	}
	return view, created, nil
}

// CancelRegistration cancels an active event registration.
func (s *TicketsService) CancelRegistration(ctx context.Context, viewerID, eventID string) error {
	var ticketID string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "id"
		FROM "Ticket"
		WHERE "eventId" = $1 AND "userId" = $2 AND "status" = 'ACTIVE'`, eventID, viewerID,
	).Scan(&ticketID)
	if errors.Is(err, pgx.ErrNoRows) {
		return httpx.NewError(404, httpx.CodeNotFound, "No active registration found.")
	}
	if err != nil {
		return fmt.Errorf("query active ticket: %w", err)
	}

	now := time.Now().UTC()
	return s.db.InTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE "Ticket"
			SET "status" = 'CANCELLED', "cancelledAt" = $1
			WHERE "id" = $2`, now, ticketID)
		if err != nil {
			return fmt.Errorf("update ticket: %w", err)
		}

		_, err = tx.Exec(ctx, `
			UPDATE "Event"
			SET "registeredCount" = GREATEST(0, "registeredCount" - 1)
			WHERE "id" = $1`, eventID)
		if err != nil {
			return fmt.Errorf("decrement registered count: %w", err)
		}
		return nil
	})
}

// MyTickets lists tickets for the current viewer with pagination.
func (s *TicketsService) MyTickets(ctx context.Context, viewerID string, page httpx.Page) (httpx.PageResult[TicketView], error) {
	baseQuery := `
		SELECT t."id", t."ticketCode", t."status", t."registeredAt", t."cancelledAt", t."checkedInAt", t."createdAt",
		       e."id", e."title", e."startsAt", e."endsAt", e."location", e."status",
		       u."id", u."displayName"
		FROM "Ticket" t
		JOIN "Event" e ON e."id" = t."eventId"
		JOIN "User" u ON u."id" = t."userId"
		WHERE t."userId" = $1`
	args := []any{viewerID}

	if page.Cursor != nil {
		args = append(args, page.Cursor.CreatedAt, page.Cursor.ID)
		baseQuery += fmt.Sprintf(` AND (t."createdAt" < $%d OR (t."createdAt" = $%d AND t."id" < $%d))`,
			len(args)-1, len(args)-1, len(args))
	}

	args = append(args, page.Limit+1)
	baseQuery += fmt.Sprintf(` ORDER BY t."createdAt" DESC, t."id" DESC LIMIT $%d`, len(args))

	rows, err := s.db.Pool.Query(ctx, baseQuery, args...)
	if err != nil {
		return httpx.PageResult[TicketView]{}, fmt.Errorf("query my tickets: %w", err)
	}
	defer rows.Close()

	var list []TicketView
	for rows.Next() {
		var t TicketView
		if err := rows.Scan(
			&t.ID, &t.TicketCode, &t.Status, &t.RegisteredAt, &t.CancelledAt, &t.CheckedInAt, &t.CreatedAt,
			&t.Event.ID, &t.Event.Title, &t.Event.StartsAt, &t.Event.EndsAt, &t.Event.Location, &t.Event.Status,
			&t.Attendee.ID, &t.Attendee.DisplayName,
		); err != nil {
			return httpx.PageResult[TicketView]{}, fmt.Errorf("scan ticket: %w", err)
		}
		list = append(list, t)
	}
	if err := rows.Err(); err != nil {
		return httpx.PageResult[TicketView]{}, err
	}

	return httpx.ToPage(list, page.Limit), nil
}

// Detail returns a ticket view if viewer is owner or organizer.
func (s *TicketsService) Detail(ctx context.Context, viewerID, ticketID string) (TicketView, error) {
	var t TicketView
	var organizerID, ticketUserID string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT t."id", t."ticketCode", t."status", t."registeredAt", t."cancelledAt", t."checkedInAt", t."createdAt",
		       t."userId", e."organizerId",
		       e."id", e."title", e."startsAt", e."endsAt", e."location", e."status",
		       u."id", u."displayName"
		FROM "Ticket" t
		JOIN "Event" e ON e."id" = t."eventId"
		JOIN "User" u ON u."id" = t."userId"
		WHERE t."id" = $1`, ticketID,
	).Scan(
		&t.ID, &t.TicketCode, &t.Status, &t.RegisteredAt, &t.CancelledAt, &t.CheckedInAt, &t.CreatedAt,
		&ticketUserID, &organizerID,
		&t.Event.ID, &t.Event.Title, &t.Event.StartsAt, &t.Event.EndsAt, &t.Event.Location, &t.Event.Status,
		&t.Attendee.ID, &t.Attendee.DisplayName,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return TicketView{}, httpx.NewError(404, httpx.CodeNotFound, "Ticket not found.")
	}
	if err != nil {
		return TicketView{}, fmt.Errorf("query ticket: %w", err)
	}

	if ticketUserID != viewerID && organizerID != viewerID {
		return TicketView{}, httpx.NewError(404, httpx.CodeNotFound, "Ticket not found.")
	}
	return t, nil
}

// Verify validates and marks a ticket as checked in.
func (s *TicketsService) Verify(ctx context.Context, verifierID, eventID, ticketCode string) (TicketView, error) {
	var organizerID, eventStatus string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT "organizerId", "status"
		FROM "Event"
		WHERE "id" = $1`, eventID,
	).Scan(&organizerID, &eventStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return TicketView{}, httpx.NewError(404, httpx.CodeNotFound, "Event not found.")
	}
	if err != nil {
		return TicketView{}, fmt.Errorf("query event: %w", err)
	}

	if organizerID != verifierID {
		return TicketView{}, httpx.Forbidden("Only the event organizer can verify tickets.")
	}
	if eventStatus != "PUBLISHED" {
		return TicketView{}, httpx.Conflict(httpx.CodeRegistrationClosed, "This event is not open for check-in.")
	}

	var ticketID, ticketEventID, currentStatus string
	err = s.db.Pool.QueryRow(ctx, `
		SELECT "id", "eventId", "status"
		FROM "Ticket"
		WHERE "ticketCode" = $1`, ticketCode,
	).Scan(&ticketID, &ticketEventID, &currentStatus)
	if errors.Is(err, pgx.ErrNoRows) || ticketEventID != eventID {
		return TicketView{}, httpx.NewError(404, httpx.CodeNotFound, "Ticket not found for this event.")
	}
	if err != nil {
		return TicketView{}, fmt.Errorf("query ticket code: %w", err)
	}

	if currentStatus == "CHECKED_IN" {
		return TicketView{}, httpx.Conflict(httpx.CodeConflict, "Ticket has already been checked in.")
	}
	if currentStatus != "ACTIVE" {
		return TicketView{}, httpx.Conflict(httpx.CodeConflict, "Ticket is not active.")
	}

	now := time.Now().UTC()
	cmd, err := s.db.Pool.Exec(ctx, `
		UPDATE "Ticket"
		SET "status" = 'CHECKED_IN', "checkedInAt" = $1
		WHERE "id" = $2 AND "status" = 'ACTIVE'`, now, ticketID)
	if err != nil {
		return TicketView{}, fmt.Errorf("check in ticket: %w", err)
	}
	if cmd.RowsAffected() != 1 {
		return TicketView{}, httpx.Conflict(httpx.CodeConflict, "Ticket has already been checked in.")
	}

	view, err := s.fetchTicketView(ctx, ticketID)
	if err != nil {
		return TicketView{}, err
	}
	checkedIn := true
	view.CheckedIn = &checkedIn
	return view, nil
}
