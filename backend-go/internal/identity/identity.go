package identity

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"campus-hive/backend-go/internal/httpx"
)

type Principal struct {
	Subject string
	Role    string
	JTI     string
}

type Verifier interface {
	VerifyAccessToken(raw string) (Principal, error)
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

func PrincipalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

func PrincipalFromRequest(r *http.Request) Principal {
	principal, _ := PrincipalFrom(r.Context())
	return principal
}

func Authenticate(verifier Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := splitBearer(r.Header.Get("Authorization"))
			if !ok {
				httpx.WriteError(w, httpx.Unauthorized(httpx.CodeInvalidCredentials, "Authentication required."))
				return
			}
			principal, err := verifier.VerifyAccessToken(raw)
			if err != nil {
				httpx.WriteError(w, httpx.Unauthorized(httpx.CodeExpiredToken, "Invalid or expired access token."))
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), principal)))
		})
	}
}

func RequireRoles(roles ...string) func(http.Handler) http.Handler {
	allowed := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := PrincipalFrom(r.Context())
			if !ok {
				httpx.WriteError(w, httpx.Unauthorized(httpx.CodeInvalidCredentials, "Authentication required."))
				return
			}
			if _, ok := allowed[principal.Role]; !ok {
				httpx.WriteError(w, httpx.Forbidden("You do not have permission to perform this action."))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

var ErrInvalidToken = errors.New("invalid or expired access token")

func splitBearer(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	scheme, token, found := strings.Cut(header, " ")
	if !found {
		return "", false
	}
	if !strings.EqualFold(scheme, "bearer") || token == "" {
		return "", false
	}
	return token, true
}
