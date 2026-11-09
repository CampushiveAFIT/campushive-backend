package events

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// EventOrganizer represents the event organizer summary.
type EventOrganizer struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// MyTicketSummary is attached to event detail views when viewer has a ticket.
type MyTicketSummary struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// EventView is the public projection of an Event.
type EventView struct {
	ID                   string           `json:"id"`
	Organizer            EventOrganizer   `json:"organizer"`
	UniversityID         string           `json:"universityId"`
	CommunityID          *string          `json:"communityId"`
	Title                string           `json:"title"`
	Description          *string          `json:"description"`
	Type                 string           `json:"type"`
	Location             *string          `json:"location"`
	StartsAt             time.Time        `json:"startsAt"`
	EndsAt               *time.Time       `json:"endsAt"`
	Capacity             int              `json:"capacity"`
	RegisteredCount      int              `json:"registeredCount"`
	AvailableSpots       int              `json:"availableSpots"`
	RegistrationDeadline *time.Time       `json:"registrationDeadline"`
	Visibility           string           `json:"visibility"`
	Status               string           `json:"status"`
	CreatedAt            time.Time        `json:"createdAt"`
	UpdatedAt            time.Time        `json:"updatedAt"`
	MyTicket             *MyTicketSummary `json:"myTicket,omitempty"`
}

func (e EventView) CursorKey() (time.Time, string) {
	return e.StartsAt, e.ID
}

// TicketEventSummary is the event subset embedded in TicketView.
type TicketEventSummary struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	StartsAt time.Time  `json:"startsAt"`
	EndsAt   *time.Time `json:"endsAt"`
	Location *string    `json:"location"`
	Status   string     `json:"status"`
}

// TicketAttendeeSummary is the user subset embedded in TicketView.
type TicketAttendeeSummary struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
}

// TicketView is the public projection of a Ticket.
type TicketView struct {
	ID           string                `json:"id"`
	TicketCode   string                `json:"ticketCode"`
	Status       string                `json:"status"`
	RegisteredAt time.Time             `json:"registeredAt"`
	CancelledAt  *time.Time            `json:"cancelledAt"`
	CheckedInAt  *time.Time            `json:"checkedInAt"`
	Event        TicketEventSummary    `json:"event"`
	Attendee     TicketAttendeeSummary `json:"attendee"`
	CreatedAt    time.Time             `json:"createdAt"`
	CheckedIn    *bool                 `json:"checkedIn,omitempty"`
}

func (t TicketView) CursorKey() (time.Time, string) {
	return t.CreatedAt, t.ID
}

// CreateEventRequest holds the parsed creation input.
type CreateEventRequest struct {
	Title                string
	Description          *string
	Type                 string
	Location             *string
	StartsAt             time.Time
	EndsAt               *time.Time
	Capacity             int
	RegistrationDeadline *time.Time
	Visibility           string
	CommunityID          *string
}

// UpdateEventRequest holds the optional fields to update.
type UpdateEventRequest struct {
	Title                *string
	Description          *string
	ClearDescription     bool
	Type                 *string
	Location             *string
	ClearLocation        bool
	StartsAt             *time.Time
	EndsAt               *time.Time
	ClearEndsAt          bool
	Capacity             *int
	RegistrationDeadline *time.Time
	ClearDeadline        bool
	Visibility           *string
	CommunityID          *string
	ClearCommunity       bool
	Status               *string
}

// EventListQuery holds event list filters.
type EventListQuery struct {
	Mine         bool
	Status       *string
	UniversityID *string
	CommunityID  *string
	Upcoming     bool
}

// newTicketCode generates a 32-byte (256-bit) base64url-encoded random string.
func newTicketCode() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
