package communities

import (
	"net/http"
	"regexp"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/validate"

	"github.com/go-chi/chi/v5"
)

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

	communityTypes = []string{
		"DEPARTMENT", "FACULTY", "HALL", "CLASS", "CLUB", "SOCIETY",
		"SPORTS", "GAMING", "PROJECT", "INTEREST", "OTHER",
	}
	communityVisibilities = []string{"PUBLIC", "UNIVERSITY", "PRIVATE"}

	msgType    = "type must be one of the following values: " + "DEPARTMENT, FACULTY, HALL, CLASS, CLUB, SOCIETY, SPORTS, GAMING, PROJECT, INTEREST, OTHER"
	msgVisible = "visibility must be one of the following values: PUBLIC, UNIVERSITY, PRIVATE"
)

type Handler struct {
	service *Service
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{service: NewService(db)}
}

// Register mounts all community routes.
func (h *Handler) Register(communities chi.Router) {
	communities.Post("/", h.create)
	communities.Get("/{id}", h.detail)
	communities.Patch("/{id}", h.update)
	communities.Delete("/{id}", h.deactivate)
	communities.Post("/{id}/join", h.join)
	communities.Delete("/{id}/leave", h.leave)
	communities.Get("/{id}/members", h.listMembers)
	communities.Patch("/{id}/members/{userId}", h.updateMemberRole)
	communities.Delete("/{id}/members/{userId}", h.removeMember)
}

// requireUUID returns a validated UUID path parameter or writes a 400 and returns empty string.
func requireUUID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	return httpx.RequireUUIDPath(w, r, param)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	body, err := httpx.DecodeBody(r, "universityId", "name", "slug", "description", "type", "visibility")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	universityID := body.String("universityId")
	name := body.String("name")
	slug := body.String("slug")
	description := body.String("description").Optional()
	ctype := body.String("type").Optional()
	visibility := body.String("visibility").Optional()

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.UUID(universityID, 4, "")
	errs.Text(name, 1, 120, "")
	errs.TextMatch(slug, 1, 80, slugPattern, "slug must be lowercase alphanumeric with single hyphens.")
	errs.TextOnlyMax(description, 2000)
	errs.Enum(ctype, communityTypes, msgType)
	errs.Enum(visibility, communityVisibilities, msgVisible)
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	var descPtr *string
	if description.Present() && description.IsString {
		v := description.Value()
		descPtr = &v
	}
	ctypeVal := ""
	if ctype.Present() && ctype.IsString {
		ctypeVal = ctype.Value()
	}
	visVal := ""
	if visibility.Present() && visibility.IsString {
		visVal = visibility.Value()
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	view, err := h.service.Create(r.Context(), viewerID, CreateRequest{
		UniversityID: universityID.Value(),
		Name:         name.Value(),
		Slug:         slug.Value(),
		Description:  descPtr,
		Type:         ctypeVal,
		Visibility:   visVal,
	})
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
	detail, err := h.service.Detail(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	body, err := httpx.DecodeBody(r, "name", "slug", "description", "type", "visibility")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	name := body.String("name").Optional()
	slug := body.String("slug").Optional()
	description := body.String("description").Optional()
	ctype := body.String("type").Optional()
	visibility := body.String("visibility").Optional()

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.Text(name, 1, 120, "")
	errs.TextMatch(slug, 1, 80, slugPattern, "slug must be lowercase alphanumeric with single hyphens.")
	errs.TextOnlyMax(description, 2000)
	errs.Enum(ctype, communityTypes, msgType)
	errs.Enum(visibility, communityVisibilities, msgVisible)
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	req := UpdateRequest{}
	if name.Present() && name.IsString {
		v := name.Value()
		req.Name = &v
	}
	if slug.Present() && slug.IsString {
		v := slug.Value()
		req.Slug = &v
	}
	if description.Present() {
		req.DescriptionSet = true
		if description.IsString {
			v := description.Value()
			req.Description = &v
		}
	}
	if ctype.Present() && ctype.IsString {
		v := ctype.Value()
		req.Type = &v
	}
	if visibility.Present() && visibility.IsString {
		v := visibility.Value()
		req.Visibility = &v
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	view, err := h.service.Update(r.Context(), viewerID, id, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) deactivate(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	view, err := h.service.Deactivate(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

func (h *Handler) join(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	m, err := h.service.Join(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) leave(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Leave(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) listMembers(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.service.ListMembers(r.Context(), viewerID, id, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) updateMemberRole(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	targetUserID, ok := requireUUID(w, r, "userId")
	if !ok {
		return
	}

	body, err := httpx.DecodeBody(r, "role")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	roleField := body.String("role")

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.Enum(roleField, []string{"MEMBER", "MODERATOR"}, "role must be MEMBER or MODERATOR.")
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	m, err := h.service.UpdateMemberRole(r.Context(), viewerID, id, targetUserID, roleField.Value())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}

func (h *Handler) removeMember(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	targetUserID, ok := requireUUID(w, r, "userId")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	m, err := h.service.RemoveMember(r.Context(), viewerID, id, targetUserID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, m)
}
