package universities

import (
	"net/http"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/validate"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{service: NewService(db)}
}

// Register mounts university and campus-identity routes.
func (h *Handler) Register(universities chi.Router, users chi.Router) {
	universities.Get("/", h.list)
	universities.Get("/{slug}", h.detail)
	universities.Get("/{slug}/communities", h.listCommunities)

	// Campus identity (Bearer required) — mounted on /users.
	users.Get("/me/university", h.myUniversity)
	users.Post("/me/university", h.joinUniversity)
	users.Patch("/me/university", h.joinUniversity) // identical logic
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	result, err := h.service.List(r.Context(), page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	detail, err := h.service.GetBySlug(r.Context(), slug)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

func (h *Handler) listCommunities(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.ListCommunities(r.Context(), slug, viewerID, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) myUniversity(w http.ResponseWriter, r *http.Request) {
	viewerID := identity.PrincipalFromRequest(r).Subject
	m, err := h.service.MyUniversity(r.Context(), viewerID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) joinUniversity(w http.ResponseWriter, r *http.Request) {
	body, err := httpx.DecodeBody(r, "universityId")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	universityID := body.String("universityId")

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.UUID(universityID, 4, "universityId must be a valid UUID.")
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	m, err := h.service.JoinOrSwitch(r.Context(), viewerID, universityID.Value())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}
