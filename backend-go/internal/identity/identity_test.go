package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubVerifier struct {
	principal Principal
	err       error
	raw       string
}

func (s *stubVerifier) VerifyAccessToken(raw string) (Principal, error) {
	s.raw = raw
	return s.principal, s.err
}

func okHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFrom(r.Context())
		if !ok {
			t.Error("the principal must reach the handler")
		}
		w.Header().Set("X-Role", principal.Role)
		w.WriteHeader(http.StatusOK)
	})
}

func call(t *testing.T, handler http.Handler, method, authorization string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "/api/v1/users/me", nil)
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) (int, string, string) {
	t.Helper()
	var payload struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("body is not an envelope: %s", rec.Body.String())
	}
	if payload.Success {
		t.Errorf("success = true, want false: %s", rec.Body.String())
	}
	return rec.Code, payload.Error.Code, payload.Error.Message
}

func TestAuthenticateRejectsMalformedAuthorizationHeaders(t *testing.T) {
	verifier := &stubVerifier{principal: Principal{Subject: "u", Role: "STUDENT"}}
	handler := Authenticate(verifier)(okHandler(t))

	cases := []struct {
		name   string
		header string
	}{
		{name: "missing header", header: ""},
		{name: "scheme only", header: "Bearer"},
		{name: "empty token", header: "Bearer "},
		{name: "basic scheme", header: "Basic abc"},
		{name: "custom scheme", header: "Token abc"},
		{name: "token without scheme", header: "abc.def.ghi"},
		{name: "scheme glued to token", header: "Bearerabc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := call(t, handler, http.MethodGet, tc.header)
			status, code, message := decodeError(t, rec)
			if status != http.StatusUnauthorized || code != "INVALID_CREDENTIALS" {
				t.Errorf("status/code = %d/%s, want 401/INVALID_CREDENTIALS", status, code)
			}
			if message != "Authentication required." {
				t.Errorf("message = %q", message)
			}
		})
	}
	if verifier.raw != "" {
		t.Errorf("the verifier must not see an unparsable token, got %q", verifier.raw)
	}
}

func TestAuthenticateReportsVerifierFailuresAsExpiredTokens(t *testing.T) {
	verifier := &stubVerifier{err: ErrInvalidToken}
	handler := Authenticate(verifier)(okHandler(t))

	for _, header := range []string{"Bearer not-a-jwt", "bearer not-a-jwt", "BEARER not-a-jwt"} {
		rec := call(t, handler, http.MethodGet, header)
		status, code, message := decodeError(t, rec)
		if status != http.StatusUnauthorized || code != "EXPIRED_TOKEN" {
			t.Errorf("%q: status/code = %d/%s, want 401/EXPIRED_TOKEN", header, status, code)
		}
		if message != "Invalid or expired access token." {
			t.Errorf("%q: message = %q", header, message)
		}
	}
	if verifier.raw != "not-a-jwt" {
		t.Errorf("the raw token must reach the verifier, got %q", verifier.raw)
	}
}

func TestAuthenticateStoresTheVerifiedPrincipal(t *testing.T) {
	verifier := &stubVerifier{principal: Principal{Subject: "user-1", Role: "STAFF", JTI: "jti-1"}}
	handler := Authenticate(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal := PrincipalFromRequest(r)
		payload, _ := json.Marshal(principal)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))

	rec := call(t, handler, http.MethodGet, "Bearer good.token.value")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	want := `{"Subject":"user-1","Role":"STAFF","JTI":"jti-1"}`
	if strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("principal = %s, want %s", rec.Body.String(), want)
	}
}

func TestPrincipalLookupOutsideTheGuard(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	if _, ok := PrincipalFrom(request.Context()); ok {
		t.Error("an anonymous request must not carry a principal")
	}
	if principal := PrincipalFromRequest(request); principal.Subject != "" || principal.Role != "" {
		t.Errorf("principal = %+v, want the zero value", principal)
	}

	ctx := WithPrincipal(context.Background(), Principal{Subject: "s", Role: "MODERATOR"})
	principal, ok := PrincipalFrom(ctx)
	if !ok || principal.Role != "MODERATOR" {
		t.Errorf("principal = %+v, ok = %v", principal, ok)
	}
}

func TestRequireRolesUsesTheSignedRoleOnly(t *testing.T) {
	handler := RequireRoles("STAFF", "MODERATOR")(okHandler(t))

	staff := Authenticate(&stubVerifier{principal: Principal{Subject: "s", Role: "STAFF"}})(handler)
	if rec := call(t, staff, http.MethodGet, "Bearer token"); rec.Code != http.StatusOK {
		t.Errorf("a STAFF principal was rejected: %d %s", rec.Code, rec.Body.String())
	}
	if rec := call(t, staff, http.MethodGet, "Bearer token"); rec.Header().Get("X-Role") != "STAFF" {
		t.Errorf("role = %q", rec.Header().Get("X-Role"))
	}

	student := Authenticate(&stubVerifier{principal: Principal{Subject: "s", Role: "STUDENT"}})(handler)
	rec := call(t, student, http.MethodGet, "Bearer token")
	status, code, message := decodeError(t, rec)
	if status != http.StatusForbidden || code != "FORBIDDEN" {
		t.Errorf("status/code = %d/%s, want 403/FORBIDDEN", status, code)
	}
	if message != "You do not have permission to perform this action." {
		t.Errorf("message = %q", message)
	}

	anonymous := call(t, handler, http.MethodGet, "")
	if status, code, _ := decodeError(t, anonymous); status != http.StatusUnauthorized || code != "INVALID_CREDENTIALS" {
		t.Errorf("anonymous status/code = %d/%s, want 401/INVALID_CREDENTIALS", status, code)
	}
}

func TestRequireRolesRejectsAnEmptyRole(t *testing.T) {
	handler := RequireRoles("STAFF")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a principal without a role must not pass")
	}))
	for _, role := range []string{"", "staff", "STAFF ", "UNKNOWN"} {
		guarded := Authenticate(&stubVerifier{principal: Principal{Subject: "s", Role: role}})(handler)
		if rec := call(t, guarded, http.MethodGet, "Bearer token"); rec.Code != http.StatusForbidden {
			t.Errorf("role %q = %d %s", role, rec.Code, rec.Body.String())
		}
	}
}
