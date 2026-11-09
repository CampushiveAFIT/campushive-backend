package notifications

import (
	"encoding/json"
	"net/http"
	"strings"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"

	"campus-hive/backend-go/internal/identity"
	"github.com/go-chi/chi/v5"
)

// Handler holds the HTTP handlers for the notifications module.
type Handler struct {
	service *Service
}

// NewHandler constructs a Handler.
func NewHandler(db *database.DB) *Handler {
	return &Handler{service: NewService(db)}
}

// Register wires the notification routes onto router.
func (h *Handler) Register(notifications, users chi.Router) {
	notifications.Get("/", h.list)
	notifications.Get("/unread-count", h.unreadCount)
	notifications.Post("/read-all", h.readAll)
	notifications.Post("/{id}/read", h.markRead)

	users.Get("/me/notification-preferences", h.getPreferences)
	users.Patch("/me/notification-preferences", h.updatePreferences)
}

func requireUUID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	return httpx.RequireUUIDPath(w, r, param)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	unreadStr := r.URL.Query().Get("unread")
	unreadOnly := unreadStr == "true" || unreadStr == "1"

	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.List(r.Context(), viewerID, page, unreadOnly)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) unreadCount(w http.ResponseWriter, r *http.Request) {
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.UnreadCount(r.Context(), viewerID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) readAll(w http.ResponseWriter, r *http.Request) {
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.MarkAllRead(r.Context(), viewerID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	notification, err := h.service.MarkRead(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, notification)
}

func (h *Handler) getPreferences(w http.ResponseWriter, r *http.Request) {
	viewerID := identity.PrincipalFromRequest(r).Subject
	prefs, err := h.service.GetPreferences(r.Context(), viewerID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prefs)
}

func (h *Handler) updatePreferences(w http.ResponseWriter, r *http.Request) {
	// Parse the body as raw JSON so we can distinguish absent vs. false.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		// Empty body is OK – no changes.
		raw = map[string]json.RawMessage{}
	}

	var errs []string
	req := UpdatePreferencesRequest{}

	known := []string{"likesEnabled", "commentsEnabled", "followsEnabled", "eventUpdatesEnabled"}
	for _, key := range known {
		v, ok := raw[key]
		if !ok {
			continue
		}
		// Reject non-boolean values.
		s := strings.TrimSpace(string(v))
		if s != "true" && s != "false" {
			errs = append(errs, key+" must be a boolean.")
			continue
		}
		b := s == "true"
		switch key {
		case "likesEnabled":
			req.LikesEnabled = &b
		case "commentsEnabled":
			req.CommentsEnabled = &b
		case "followsEnabled":
			req.FollowsEnabled = &b
		case "eventUpdatesEnabled":
			req.EventUpdatesEnabled = &b
		}
	}

	// Report unknown keys.
	allowed := map[string]bool{}
	for _, k := range known {
		allowed[k] = true
	}
	for k := range raw {
		if !allowed[k] {
			errs = append(errs, "property "+k+" should not exist.")
		}
	}

	if len(errs) > 0 {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	prefs, err := h.service.UpdatePreferences(r.Context(), viewerID, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, prefs)
}
