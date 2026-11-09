package events

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/validate"

	"github.com/go-chi/chi/v5"
)

var (
	uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

	validTypes = map[string]bool{
		"ACADEMIC": true, "CAREER": true, "CULTURAL": true, "SPORTS": true,
		"SOCIAL": true, "WORKSHOP": true, "COMPETITION": true, "VOLUNTEERING": true,
		"PARTY": true, "OTHER": true,
	}
	validVisibilities = map[string]bool{
		"PUBLIC": true, "UNIVERSITY": true, "COMMUNITY": true,
	}
	validStatuses = map[string]bool{
		"DRAFT": true, "PUBLISHED": true, "CANCELLED": true, "COMPLETED": true,
	}
)

// Handler serves HTTP endpoints for events and tickets.
type Handler struct {
	events  *Service
	tickets *TicketsService
}

// NewHandler constructs an event Handler.
func NewHandler(db *database.DB) *Handler {
	svc := NewService(db)
	return &Handler{
		events:  svc,
		tickets: NewTicketsService(db, svc),
	}
}

// Register mounts all event and ticket routes.
func (h *Handler) Register(events, tickets, users, universities, communities chi.Router) {
	events.Post("/", h.create)
	events.Get("/", h.list)
	events.Get("/{id}", h.detail)
	events.Patch("/{id}", h.update)
	events.Delete("/{id}", h.cancel)
	events.Post("/{id}/register", h.register)
	events.Delete("/{id}/register", h.cancelRegistration)
	events.Post("/{id}/verify", h.verify)

	universities.Get("/{slug}/events", h.listByUniversity)
	communities.Get("/{id}/events", h.listByCommunity)
	users.Get("/me/tickets", h.myTickets)
	tickets.Get("/{id}", h.ticketDetail)
}

func requireUUID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	return httpx.RequireUUIDPath(w, r, param)
}

func parseTime(val string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, val)
	if err != nil {
		t, err = time.Parse(time.RFC3339, val)
		if err != nil {
			return time.Time{}, false
		}
	}
	return t.UTC(), true
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	body, err := httpx.DecodeBody(r,
		"title", "description", "type", "location", "startsAt", "endsAt",
		"capacity", "registrationDeadline", "visibility", "communityId",
	)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	title := body.String("title").Transform(validate.Trim)
	description := body.String("description").Transform(validate.Trim).Optional()
	eType := body.String("type").Optional()
	location := body.String("location").Transform(validate.Trim).Optional()
	startsAtField := body.String("startsAt")
	endsAtField := body.String("endsAt").Optional()
	regDeadlineField := body.String("registrationDeadline").Optional()
	visibilityField := body.String("visibility").Optional()
	communityIDField := body.String("communityId").Optional()

	var errs validate.Errors
	errs.Whitelist(body.Unknown())

	errs.IsString(title)
	errs.MinLength(title, 1, "title must not be empty.")
	errs.MaxLength(title, 200)

	errs.MaxLength(description, 5000)
	errs.MaxLength(location, 500)

	if eType.Present() && eType.IsString && !validTypes[strings.ToUpper(eType.Value())] {
		errs.Add("type must be a valid EventType.")
	}

	var startsAt time.Time
	errs.IsString(startsAtField)
	if startsAtField.IsString {
		t, ok := parseTime(startsAtField.Value())
		if !ok {
			errs.Add("startsAt must be a valid ISO date.")
		} else {
			startsAt = t
		}
	}

	var endsAt *time.Time
	if endsAtField.Present() && endsAtField.IsString {
		t, ok := parseTime(endsAtField.Value())
		if !ok {
			errs.Add("endsAt must be a valid ISO date.")
		} else {
			endsAt = &t
		}
	}

	var deadline *time.Time
	if regDeadlineField.Present() && regDeadlineField.IsString {
		t, ok := parseTime(regDeadlineField.Value())
		if !ok {
			errs.Add("registrationDeadline must be a valid ISO date.")
		} else {
			deadline = &t
		}
	}

	var capacity int
	capRaw := body.Raw("capacity")
	if len(capRaw) == 0 {
		errs.Add("capacity must be an integer number.")
	} else {
		var f float64
		if err := json.Unmarshal(capRaw, &f); err != nil || float64(int(f)) != f {
			errs.Add("capacity must be an integer number.")
		} else {
			capacity = int(f)
			if capacity < 1 {
				errs.Add("capacity must be at least 1.")
			} else if capacity > 100000 {
				errs.Add("capacity must not exceed 100000.")
			}
		}
	}

	if visibilityField.Present() && visibilityField.IsString && !validVisibilities[strings.ToUpper(visibilityField.Value())] {
		errs.Add("visibility must be PUBLIC, UNIVERSITY, or COMMUNITY.")
	}

	if communityIDField.Present() && communityIDField.IsString && !uuidPattern.MatchString(communityIDField.Value()) {
		errs.Add("communityId must be a valid UUID.")
	}

	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	req := CreateEventRequest{
		Title:                title.Value(),
		StartsAt:             startsAt,
		EndsAt:               endsAt,
		Capacity:             capacity,
		RegistrationDeadline: deadline,
		Type:                 "OTHER",
		Visibility:           "UNIVERSITY",
	}
	if description.Present() && description.IsString && description.Value() != "" {
		v := description.Value()
		req.Description = &v
	}
	if eType.Present() && eType.IsString {
		req.Type = strings.ToUpper(eType.Value())
	}
	if location.Present() && location.IsString && location.Value() != "" {
		v := location.Value()
		req.Location = &v
	}
	if visibilityField.Present() && visibilityField.IsString {
		req.Visibility = strings.ToUpper(visibilityField.Value())
	}
	if communityIDField.Present() && communityIDField.IsString {
		v := communityIDField.Value()
		req.CommunityID = &v
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	view, err := h.events.Create(r.Context(), viewerID, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, view)
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	view, err := h.events.Detail(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}

	body, err := httpx.DecodeBody(r,
		"title", "description", "type", "location", "startsAt", "endsAt",
		"capacity", "registrationDeadline", "visibility", "communityId", "status",
	)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	title := body.String("title").Transform(validate.Trim).Optional()
	description := body.String("description").Transform(validate.Trim).Optional()
	eType := body.String("type").Optional()
	location := body.String("location").Transform(validate.Trim).Optional()
	startsAtField := body.String("startsAt").Optional()
	endsAtField := body.String("endsAt").Optional()
	regDeadlineField := body.String("registrationDeadline").Optional()
	visibilityField := body.String("visibility").Optional()
	communityIDField := body.String("communityId").Optional()
	statusField := body.String("status").Optional()

	var errs validate.Errors
	errs.Whitelist(body.Unknown())

	if title.Present() {
		errs.IsString(title)
		errs.MinLength(title, 1, "title must not be empty.")
		errs.MaxLength(title, 200)
	}
	errs.MaxLength(description, 5000)
	errs.MaxLength(location, 500)

	if eType.Present() && eType.IsString && !validTypes[strings.ToUpper(eType.Value())] {
		errs.Add("type must be a valid EventType.")
	}

	var startsAt *time.Time
	if startsAtField.Present() && startsAtField.IsString {
		t, ok := parseTime(startsAtField.Value())
		if !ok {
			errs.Add("startsAt must be a valid ISO date.")
		} else {
			startsAt = &t
		}
	}

	var endsAt *time.Time
	if endsAtField.Present() && !endsAtField.IsNull && endsAtField.IsString {
		t, ok := parseTime(endsAtField.Value())
		if !ok {
			errs.Add("endsAt must be a valid ISO date.")
		} else {
			endsAt = &t
		}
	}

	var deadline *time.Time
	if regDeadlineField.Present() && !regDeadlineField.IsNull && regDeadlineField.IsString {
		t, ok := parseTime(regDeadlineField.Value())
		if !ok {
			errs.Add("registrationDeadline must be a valid ISO date.")
		} else {
			deadline = &t
		}
	}

	var capacity *int
	capRaw := body.Raw("capacity")
	if len(capRaw) > 0 {
		var f float64
		if err := json.Unmarshal(capRaw, &f); err != nil || float64(int(f)) != f {
			errs.Add("capacity must be an integer number.")
		} else {
			c := int(f)
			if c < 1 {
				errs.Add("capacity must be at least 1.")
			} else if c > 100000 {
				errs.Add("capacity must not exceed 100000.")
			} else {
				capacity = &c
			}
		}
	}

	if visibilityField.Present() && visibilityField.IsString && !validVisibilities[strings.ToUpper(visibilityField.Value())] {
		errs.Add("visibility must be PUBLIC, UNIVERSITY, or COMMUNITY.")
	}

	if communityIDField.Present() && !communityIDField.IsNull && communityIDField.IsString && !uuidPattern.MatchString(communityIDField.Value()) {
		errs.Add("communityId must be a valid UUID.")
	}

	if statusField.Present() && statusField.IsString && !validStatuses[strings.ToUpper(statusField.Value())] {
		errs.Add("status must be DRAFT, PUBLISHED, CANCELLED, or COMPLETED.")
	}

	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	req := UpdateEventRequest{
		StartsAt:             startsAt,
		EndsAt:               endsAt,
		ClearEndsAt:          endsAtField.IsNull,
		RegistrationDeadline: deadline,
		ClearDeadline:        regDeadlineField.IsNull,
		ClearDescription:     description.IsNull,
		ClearLocation:        location.IsNull,
		ClearCommunity:       communityIDField.IsNull,
		Capacity:             capacity,
	}

	if title.Present() && title.IsString {
		v := title.Value()
		req.Title = &v
	}
	if description.Present() && description.IsString {
		v := description.Value()
		req.Description = &v
	}
	if eType.Present() && eType.IsString {
		v := strings.ToUpper(eType.Value())
		req.Type = &v
	}
	if location.Present() && location.IsString {
		v := location.Value()
		req.Location = &v
	}
	if visibilityField.Present() && visibilityField.IsString {
		v := strings.ToUpper(visibilityField.Value())
		req.Visibility = &v
	}
	if communityIDField.Present() && communityIDField.IsString {
		v := communityIDField.Value()
		req.CommunityID = &v
	}
	if statusField.Present() && statusField.IsString {
		v := strings.ToUpper(statusField.Value())
		req.Status = &v
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	view, err := h.events.Update(r.Context(), viewerID, id, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	view, err := h.events.Cancel(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	q := r.URL.Query()
	query := EventListQuery{
		Mine:     q.Get("mine") == "true" || q.Get("mine") == "1",
		Upcoming: q.Get("upcoming") == "true" || q.Get("upcoming") == "1",
	}
	if status := q.Get("status"); status != "" {
		st := strings.ToUpper(status)
		query.Status = &st
	}
	if univID := q.Get("universityId"); univID != "" {
		if !uuidPattern.MatchString(univID) {
			httpx.WriteError(w, httpx.ValidationError([]string{"universityId must be a valid UUID."}))
			return
		}
		query.UniversityID = &univID
	}
	if commID := q.Get("communityId"); commID != "" {
		if !uuidPattern.MatchString(commID) {
			httpx.WriteError(w, httpx.ValidationError([]string{"communityId must be a valid UUID."}))
			return
		}
		query.CommunityID = &commID
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.events.List(r.Context(), viewerID, query, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) listByUniversity(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.events.ListByUniversity(r.Context(), viewerID, slug, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) listByCommunity(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.events.ListByCommunity(r.Context(), viewerID, id, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	ticket, created, err := h.tickets.Register(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, ticket)
}

func (h *Handler) cancelRegistration(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	if err := h.tickets.CancelRegistration(r.Context(), viewerID, id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

func (h *Handler) myTickets(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.tickets.MyTickets(r.Context(), viewerID, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) ticketDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	ticket, err := h.tickets.Detail(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ticket)
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}

	body, err := httpx.DecodeBody(r, "ticketCode")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	ticketCode := body.String("ticketCode")

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.IsString(ticketCode)
	errs.MinLength(ticketCode, 1, "ticketCode must not be empty.")
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	verifierID := identity.PrincipalFromRequest(r).Subject
	result, err := h.tickets.Verify(r.Context(), verifierID, id, ticketCode.Value())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}
