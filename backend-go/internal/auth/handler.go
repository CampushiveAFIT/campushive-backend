package auth

import (
	"net/http"

	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/identity"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
	limiter *Limiter
}

func NewHandler(service *Service, limiter *Limiter) *Handler {
	return &Handler{service: service, limiter: limiter}
}

func (h *Handler) Register(router chi.Router, tokens *TokenService) {
	router.Route("/auth", func(auth chi.Router) {
		auth.With(h.limiter.Middleware(LimitOptions{Limit: 5, WindowSec: 900, Key: "register"})).
			Post("/register", h.register)
		auth.With(h.limiter.Middleware(LimitOptions{Limit: 5, WindowSec: 900, Key: "login"})).
			Post("/login", h.login)
		auth.With(h.limiter.Middleware(LimitOptions{Limit: 10, WindowSec: 900, Key: "refresh"})).
			Post("/refresh", h.refresh)
		auth.With(h.limiter.Middleware(LimitOptions{Limit: 3, WindowSec: 900, Key: "password-reset"})).
			Post("/password/reset/request", h.requestPasswordReset)
		auth.Post("/password/reset/confirm", h.confirmPasswordReset)
		auth.With(h.limiter.Middleware(LimitOptions{Limit: 3, WindowSec: 900, Key: "email-verify"})).
			Post("/email/verify/request", h.requestEmailVerification)
		auth.Post("/email/verify/confirm", h.confirmEmailVerification)
		auth.Post("/logout", h.logout)
		auth.With(identity.Authenticate(tokens)).Post("/password/change", h.changePassword)
	})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	req, err := DecodeRegister(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	user, err := h.service.Register(r.Context(), req)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, user)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	req, err := DecodeLogin(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	result, err := h.service.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	req, err := DecodeRefreshToken(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	result, err := h.service.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, result)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	req, err := DecodeRefreshToken(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	revoked, err := h.service.Logout(r.Context(), req.RefreshToken)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"revoked": revoked})
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	req, err := DecodePasswordChange(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	principal := identity.PrincipalFromRequest(r)
	if err := h.service.ChangePassword(r.Context(), principal.Subject, req.CurrentPassword, req.NewPassword); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"changed": true})
}

func (h *Handler) requestPasswordReset(w http.ResponseWriter, r *http.Request) {
	req, err := DecodePasswordResetRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.service.RequestPasswordReset(r.Context(), req.Email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"requested": true})
}

func (h *Handler) confirmPasswordReset(w http.ResponseWriter, r *http.Request) {
	req, err := DecodePasswordResetConfirm(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.service.ConfirmPasswordReset(r.Context(), req.Token, req.NewPassword); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"reset": true})
}

func (h *Handler) requestEmailVerification(w http.ResponseWriter, r *http.Request) {
	req, err := DecodeEmailVerifyRequest(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.service.RequestEmailVerification(r.Context(), req.Email); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]bool{"requested": true})
}

func (h *Handler) confirmEmailVerification(w http.ResponseWriter, r *http.Request) {
	req, err := DecodeEmailVerifyConfirm(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	user, err := h.service.ConfirmEmailVerification(r.Context(), req.Token)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, user)
}
