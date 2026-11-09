package graph

import (
	"net/http"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"

	"campus-hive/backend-go/internal/identity"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{service: NewService(db)}
}

// Register mounts follow/block routes on /users/:id/...
func (h *Handler) Register(users chi.Router) {
	users.Post("/{id}/follow", h.follow)
	users.Delete("/{id}/follow", h.unfollow)
	users.Get("/{id}/followers", h.followers)
	users.Get("/{id}/following", h.following)
	users.Post("/{id}/block", h.block)
	users.Delete("/{id}/block", h.unblock)
}

func requireUUID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	return httpx.RequireUUIDPath(w, r, param)
}

func (h *Handler) follow(w http.ResponseWriter, r *http.Request) {
	targetID, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Follow(r.Context(), viewerID, targetID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) unfollow(w http.ResponseWriter, r *http.Request) {
	targetID, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Unfollow(r.Context(), viewerID, targetID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) followers(w http.ResponseWriter, r *http.Request) {
	targetID, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Followers(r.Context(), viewerID, targetID, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) following(w http.ResponseWriter, r *http.Request) {
	targetID, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Following(r.Context(), viewerID, targetID, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) block(w http.ResponseWriter, r *http.Request) {
	targetID, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Block(r.Context(), viewerID, targetID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) unblock(w http.ResponseWriter, r *http.Request) {
	targetID, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Unblock(r.Context(), viewerID, targetID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}
