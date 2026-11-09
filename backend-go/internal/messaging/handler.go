package messaging

import (
	"net/http"
	"regexp"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpx"

	"campus-hive/backend-go/internal/identity"
	"campus-hive/backend-go/internal/validate"
	"github.com/go-chi/chi/v5"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Handler exposes HTTP handlers for the messaging module.
type Handler struct {
	service *Service
}

// NewHandler constructs a Handler.
func NewHandler(db *database.DB) *Handler {
	return &Handler{service: NewService(db)}
}

// Register mounts all conversation/message routes.
func (h *Handler) Register(conversations chi.Router) {
	conversations.Post("/direct", h.getOrCreateDirect)
	conversations.Get("/", h.list)
	conversations.Get("/unread-count", h.unreadCount)
	conversations.Get("/{id}", h.detail)
	conversations.Get("/{id}/messages", h.listMessages)
	conversations.Post("/{id}/messages", h.sendMessage)
	conversations.Post("/{id}/read", h.markRead)
}

func requireUUID(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	return httpx.RequireUUIDPath(w, r, param)
}

func (h *Handler) getOrCreateDirect(w http.ResponseWriter, r *http.Request) {
	body, err := httpx.DecodeBody(r, "userId")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	targetUserID := body.String("userId")

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.IsString(targetUserID)
	if targetUserID.IsString && !uuidPattern.MatchString(targetUserID.Value()) {
		errs.Add("userId must be a valid UUID.")
	}
	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	conv, created, err := h.service.GetOrCreateDirect(r.Context(), viewerID, targetUserID.Value())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, conv)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := httpx.ParsePage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.List(r.Context(), viewerID, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) unreadCount(w http.ResponseWriter, r *http.Request) {
	viewerID := identity.PrincipalFromRequest(r).Subject
	total, err := h.service.UnreadTotal(r.Context(), viewerID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]int{"unread": total})
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	conv, err := h.service.Detail(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, conv)
}

func (h *Handler) listMessages(w http.ResponseWriter, r *http.Request) {
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
	result, err := h.service.ListMessages(r.Context(), viewerID, id, page)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) sendMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}

	body, err := httpx.DecodeBody(r, "clientMessageId", "body")
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	clientMsgID := body.String("clientMessageId")
	msgBody := body.String("body").Transform(validate.Trim)

	var errs validate.Errors
	errs.Whitelist(body.Unknown())
	errs.IsString(clientMsgID)
	errs.MinLength(clientMsgID, 1, "clientMessageId must not be empty.")
	errs.MaxLength(clientMsgID, 128)

	errs.IsString(msgBody)
	errs.MinLength(msgBody, 1, "body must not be empty.")
	errs.MaxLength(msgBody, 4000)

	if err := errs.Err(); err != nil {
		httpx.WriteError(w, httpx.ValidationError(errs))
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	msg, created, err := h.service.SendMessage(r.Context(), viewerID, id, clientMsgID.Value(), msgBody.Value())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, msg)
}

func (h *Handler) markRead(w http.ResponseWriter, r *http.Request) {
	id, ok := requireUUID(w, r, "id")
	if !ok {
		return
	}

	viewerID := identity.PrincipalFromRequest(r).Subject
	result, err := h.service.MarkRead(r.Context(), viewerID, id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}
