package users_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpserver"
	"campus-hive/backend-go/internal/ratelimit"
	"campus-hive/backend-go/internal/routes"
	"campus-hive/backend-go/internal/testutil"

	"github.com/google/uuid"
)

const (
	testPassword = "Str0ng-Passw0rd!"
	apiPrefix    = "/api/v1"
)

type harness struct {
	t    *testing.T
	http *httptest.Server
	db   *database.DB
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	testutil.SkipIfNoDatabase(t)
	testutil.ResetDatabase(t)
	db := testutil.OpenDB(t)
	redisClient := testutil.StartRedis(t)
	logger := testutil.TestLogger(t)
	server := httpserver.New(httpserver.Options{
		Config:         testutil.TestConfig(t),
		Logger:         logger,
		DB:             db,
		Redis:          redisClient,
		Limiter:        ratelimit.New(redisClient.Redis(), logger),
		RegisterRoutes: routes.Register,
	})
	httpServer := httptest.NewServer(server.Router())
	t.Cleanup(httpServer.Close)
	return &harness{t: t, http: httpServer, db: db}
}

func (h *harness) request(method, path, body, token string) (int, http.Header, string) {
	h.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, h.http.URL+apiPrefix+path, reader)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	result, err := h.http.Client().Do(request)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = result.Body.Close() }()
	raw, _ := io.ReadAll(result.Body)
	return result.StatusCode, result.Header, string(raw)
}

func (h *harness) accessToken(emailAddress string) string {
	h.t.Helper()
	status, _, body := h.request(http.MethodPost, "/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, emailAddress, testPassword), "")
	if status != http.StatusCreated {
		h.t.Fatalf("register = %d %s", status, body)
	}
	status, _, body = h.request(http.MethodPost, "/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, emailAddress, testPassword), "")
	if status != http.StatusOK {
		h.t.Fatalf("login = %d %s", status, body)
	}
	var payload struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		h.t.Fatalf("decode %s: %v", body, err)
	}
	return payload.Data.AccessToken
}

func TestNullDisplayNameReproducesTheLegacyTypeError(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("nullname-" + uuid.NewString() + "@example.com")

	status, _, body := h.request(http.MethodPatch, "/users/me", `{"displayName":null}`, token)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", status, body)
	}
	want := `{"success":false,"error":{"code":"INTERNAL_ERROR","message":"An unexpected error occurred."}}`
	if body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
}

func TestProfilePatchKeepsNullAndBlankSemantics(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("profile-" + uuid.NewString() + "@example.com")

	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "blank display name clears the column", body: `{"displayName":""}`},
		{name: "whitespace display name clears it", body: `{"displayName":"   "}`},
		{name: "bio keeps an empty string", body: `{"bio":""}`},
		{name: "null bio clears it", body: `{"bio":null}`},
		{name: "null avatar clears it", body: `{"avatarUrl":null}`},
		{name: "valid avatar", body: `{"avatarUrl":"https://cdn.example.com/a.png"}`},
		{name: "unknown property", body: `{"role":"STAFF"}`, wantCode: "VALIDATION_ERROR"},
		{name: "wrong type", body: `{"bio":123}`, wantCode: "VALIDATION_ERROR"},
		{name: "blank avatar", body: `{"avatarUrl":""}`, wantCode: "VALIDATION_ERROR"},
		{name: "malformed json", body: `{"bio":`, wantCode: "VALIDATION_ERROR"},
		{name: "array body", body: `[]`},
		{name: "empty body", body: ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _, body := h.request(http.MethodPatch, "/users/me", tc.body, token)
			if tc.wantCode == "" {
				if status != http.StatusOK {
					t.Fatalf("status = %d: %s", status, body)
				}
				return
			}
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", status, body)
			}
			if !strings.Contains(body, `"code":"`+tc.wantCode+`"`) {
				t.Errorf("body = %s", body)
			}
		})
	}
}

func TestUnknownPropertyDoesNotLeakTheRole(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("whitelist-" + uuid.NewString() + "@example.com")

	status, _, body := h.request(http.MethodPatch, "/users/me", `{"role":"STAFF","bio":"x"}`, token)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d: %s", status, body)
	}
	if !strings.Contains(body, "property role should not exist") {
		t.Errorf("body = %s", body)
	}

	var role string
	if err := h.db.Pool.QueryRow(context.Background(), `SELECT "role" FROM "User" WHERE "id" = (SELECT "userId" FROM "RefreshToken" LIMIT 1)`).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	if role != "STUDENT" {
		t.Errorf("role = %q, want STUDENT", role)
	}
}
