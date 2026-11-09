package posts

import (
	"net/http"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/validate"

	"github.com/go-chi/chi/v5"
)

var postVisibilities = []string{"PUBLIC", "UNIVERSITY", "COMMUNITY", "FOLLOWERS"}

type Handler struct {
	service *Service
}

func NewHandler(db *database.DB) *Handler {
	return &Handler{service: NewService(db)}
}

func (h *Handler) Register(posts, comments, users, universities, communities chi.Router) {
	posts.Post("/", h.create)
	posts.Get("/{id}", h.detail)
	posts.Patch("/{id}", h.update)
	posts.Delete("/{id}", h.remove)
	posts.Post("/{id}/like", h.like)
	posts.Delete("/{id}/like", h.unlike)
	posts.Post("/{id}/comments", h.createComment)
	posts.Get("/{id}/comments", h.listComments)

	comments.Patch("/{id}", h.updateComment)
	comments.Delete("/{id}", h.deleteComment)

	users.Get("/{id}/posts", h.listByAuthor)
	universities.Get("/{slug}/posts", h.listByUniversity)
	communities.Get("/{id}/posts", h.listByCommunity)
}

func requireUUID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	return httpx.RequireUUIDPath(w, r, param)
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	body, err := httpx.DecodeBody(r, "content", "visibility", "communityId", "eventId")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	content := body.String("content").Transform(validate.Trim)
	visibility := body.String("visibility").Optional()
	communityID := body.String("communityId").Optional()
	eventID := body.String("eventId").Optional()

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.Text(content, 1, 5000, "content must not be empty.")
	errs.Enum(visibility, postVisibilities, "")
	errs.UUID(communityID, 4, "")
	errs.UUID(eventID, 4, "")
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	req := CreatePostRequest{Content: content.Value()}
	if visibility.Present() && visibility.IsString {
		req.Visibility = visibility.Value()
	}
	if communityID.Present() && communityID.IsString {
		v := communityID.Value()
		req.CommunityID = &v
	}
	if eventID.Present() && eventID.IsString {
		v := eventID.Value()
		req.EventID = &v
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	post, err := h.service.Create(r.Context(), viewerID, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, post)
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	post, err := h.service.Detail(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	body, err := httpx.DecodeBody(r, "content", "visibility")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	content := body.String("content").Transform(validate.Trim).Optional()
	visibility := body.String("visibility").Optional()

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.Text(content, 1, 5000, "content must not be empty.")
	errs.Enum(visibility, postVisibilities, "")
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	req := UpdatePostRequest{}
	if content.Present() && content.IsString {
		v := content.Value()
		req.Content = &v
	}
	if visibility.Present() && visibility.IsString {
		v := visibility.Value()
		req.Visibility = &v
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	post, err := h.service.Update(r.Context(), viewerID, id, req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, post)
}

func (h *Handler) remove(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Remove(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) like(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Like(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) unlike(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.Unlike(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) createComment(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	body, err := httpx.DecodeBody(r, "content")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	content := body.String("content").Transform(validate.Trim)

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.Text(content, 1, 2000, "content must not be empty.")
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	comment, err := h.service.CreateComment(r.Context(), viewerID, id, CreateCommentRequest{Content: content.Value()})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, comment)
}

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.service.ListComments(r.Context(), viewerID, id, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) updateComment(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	body, err := httpx.DecodeBody(r, "content")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	content := body.String("content").Transform(validate.Trim)

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.Text(content, 1, 2000, "content must not be empty.")
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	comment, err := h.service.UpdateComment(r.Context(), viewerID, id, UpdateCommentRequest{Content: content.Value()})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, comment)
}

func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}
	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.DeleteComment(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) listByAuthor(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.service.ListByAuthor(r.Context(), viewerID, id, page)
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
	result, err := h.service.ListByUniversity(r.Context(), viewerID, slug, page)
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
	result, err := h.service.ListByCommunity(r.Context(), viewerID, id, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}
