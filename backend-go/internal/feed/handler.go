package feed

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"

	"github.com/go-chi/chi/v5"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Handler exposes HTTP handlers for feed endpoints.
type Handler struct {
	service *Service
}

// NewHandler constructs a feed Handler.
func NewHandler(db *database.DB) *Handler {
	return &Handler{service: NewService(db)}
}

// Register mounts all feed routes.
func (h *Handler) Register(feed chi.Router) {
	feed.Get("/home", h.home)
	feed.Get("/university", h.university)
	feed.Get("/community/{id}", h.community)
	feed.Get("/explore", h.explore)
}

func parseFeedLimit(r *http.Request) (int, error) {
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 1 || n > 50 {
			return 0, httpx.ValidationMessage("limit must be an integer between 1 and 50.")
		}
		return n, nil
	}
	return 20, nil
}

func parseCursor(r *http.Request) *string {
	if c := strings.TrimSpace(r.URL.Query().Get("cursor")); c != "" {
		return &c
	}
	return nil
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	limit, err := parseFeedLimit(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	cursor := parseCursor(r)

	viewerID := identity.PrincipalFromRequest(r).Subject
	page, err := h.service.HomeFeed(r.Context(), viewerID, limit, cursor)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) university(w http.ResponseWriter, r *http.Request) {
	limit, err := parseFeedLimit(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	cursor := parseCursor(r)

	var targetUniID *string
	if rawUni := strings.TrimSpace(r.URL.Query().Get("universityId")); rawUni != "" {
		if !uuidPattern.MatchString(rawUni) {
			httpx.WriteError(w, httpx.ValidationError([]string{"universityId must be a valid UUID."}))
			return
		}
		targetUniID = &rawUni
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	page, err := h.service.UniversityFeed(r.Context(), viewerID, targetUniID, limit, cursor)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) community(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !uuidPattern.MatchString(id) {
		httpx.WriteError(w, httpx.ValidationError([]string{"id must be a valid UUID."}))
		return
	}

	limit, err := parseFeedLimit(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	cursor := parseCursor(r)

	viewerID := identity.PrincipalFromRequest(r).Subject
	page, err := h.service.CommunityFeed(r.Context(), viewerID, id, limit, cursor)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) explore(w http.ResponseWriter, r *http.Request) {
	limit, err := parseFeedLimit(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	cursor := parseCursor(r)

	viewerID := identity.PrincipalFromRequest(r).Subject
	page, err := h.service.ExploreFeed(r.Context(), viewerID, limit, cursor)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}
