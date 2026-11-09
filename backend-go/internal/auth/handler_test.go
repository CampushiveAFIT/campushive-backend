package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"campus-hive/backend-go/internal/database"
	"campus-hive/backend-go/internal/httpserver"
	"campus-hive/backend-go/internal/ratelimit"
	"campus-hive/backend-go/internal/routes"
	"campus-hive/backend-go/internal/testutil"

	"github.com/google/uuid"
)

const (
	testPassword = "Str0ng-Passw0rd!"
	newPassword  = "N3w-Passw0rd!"
	apiPrefix    = "/api/v1"

	noContentType = "-"
)

func digest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

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
	cfg := testutil.TestConfig(t)
	logger := testutil.TestLogger(t)

	server := httpserver.New(httpserver.Options{
		Config:         cfg,
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

type envelope struct {
	Success bool `json:"success"`
	Data    struct {
		ID                 string  `json:"id"`
		Email              string  `json:"email"`
		DisplayName        *string `json:"displayName"`
		AvatarURL          *string `json:"avatarUrl"`
		Bio                *string `json:"bio"`
		Role               string  `json:"role"`
		VerificationStatus string  `json:"verificationStatus"`
		EmailVerifiedAt    *string `json:"emailVerifiedAt"`
		CreatedAt          string  `json:"createdAt"`
		UpdatedAt          string  `json:"updatedAt"`
		PasswordHash       *string `json:"passwordHash"`
		IsActive           *bool   `json:"isActive"`
		User               struct {
			ID string `json:"id"`
		} `json:"user"`
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		Revoked      *bool  `json:"revoked"`
		Changed      *bool  `json:"changed"`
		Reset        *bool  `json:"reset"`
		Requested    *bool  `json:"requested"`
	} `json:"data"`
	Error struct {
		Code    string   `json:"code"`
		Message string   `json:"message"`
		Details []string `json:"details"`
	} `json:"error"`
}

type response struct {
	status  int
	headers http.Header
	body    string
}

func (r response) decode(t *testing.T) envelope {
	t.Helper()
	var out envelope
	if err := json.Unmarshal([]byte(r.body), &out); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
	return out
}

func (h *harness) post(path, body, token, contentType string) response {
	h.t.Helper()
	if contentType == "" {
		contentType = "application/json"
	}
	request, err := http.NewRequest(http.MethodPost, h.http.URL+apiPrefix+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	if contentType == noContentType {
		contentType = ""
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	result, err := h.http.Client().Do(request)
	if err != nil {
		h.t.Fatalf("post %s: %v", path, err)
	}
	defer func() { _ = result.Body.Close() }()
	raw, err := io.ReadAll(result.Body)
	if err != nil {
		h.t.Fatalf("read body: %v", err)
	}
	return response{status: result.StatusCode, headers: result.Header, body: string(raw)}
}

func (h *harness) patch(path, body, token string) response {
	h.t.Helper()
	request, err := http.NewRequest(http.MethodPatch, h.http.URL+apiPrefix+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	result, err := h.http.Client().Do(request)
	if err != nil {
		h.t.Fatalf("patch %s: %v", path, err)
	}
	defer func() { _ = result.Body.Close() }()
	raw, _ := io.ReadAll(result.Body)
	return response{status: result.StatusCode, headers: result.Header, body: string(raw)}
}

func (h *harness) get(path, token string) response {
	h.t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.http.URL+apiPrefix+path, nil)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	result, err := h.http.Client().Do(request)
	if err != nil {
		h.t.Fatalf("get %s: %v", path, err)
	}
	defer func() { _ = result.Body.Close() }()
	raw, _ := io.ReadAll(result.Body)
	return response{status: result.StatusCode, headers: result.Header, body: string(raw)}
}

func (h *harness) register(emailAddress string) response {
	h.t.Helper()
	result := h.post("/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, emailAddress, testPassword), "", "")
	if result.status != http.StatusCreated {
		h.t.Fatalf("register %s = %d %s", emailAddress, result.status, result.body)
	}
	return result
}

func (h *harness) login(emailAddress, password string) response {
	h.t.Helper()
	return h.post("/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, emailAddress, password), "", "")
}

func TestRegisterReturnsSafeUserWithoutPrivateColumns(t *testing.T) {
	h := newHarness(t)
	emailAddress := "register-" + uuid.NewString() + "@example.com"
	result := h.post("/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q,"displayName":"  Ada  "}`, emailAddress, testPassword), "", "")

	if result.status != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", result.status, result.body)
	}
	wantOrder := []string{`"id"`, `"email"`, `"displayName"`, `"avatarUrl"`, `"bio"`, `"role"`,
		`"verificationStatus"`, `"emailVerifiedAt"`, `"createdAt"`, `"updatedAt"`}
	position := -1
	for _, key := range wantOrder {
		index := strings.Index(result.body, key)
		if index < 0 {
			t.Fatalf("%s is missing from %s", key, result.body)
		}
		if index < position {
			t.Errorf("%s is out of order in %s", key, result.body)
		}
		position = index
	}
	if strings.Contains(result.body, "passwordHash") || strings.Contains(result.body, "isActive") {
		t.Errorf("the response leaks private columns: %s", result.body)
	}
	payload := result.decode(t)
	if !payload.Success || payload.Data.Role != "STUDENT" ||
		payload.Data.VerificationStatus != "UNVERIFIED" || payload.Data.EmailVerifiedAt != nil {
		t.Errorf("payload = %+v", payload)
	}
	if payload.Data.DisplayName == nil || *payload.Data.DisplayName != "Ada" {
		t.Errorf("displayName = %v", payload.Data.DisplayName)
	}
	if got := result.headers.Get("X-RateLimit-Limit"); got != "5" {
		t.Errorf("X-RateLimit-Limit = %q, want 5", got)
	}
	if got := result.headers.Get("X-RateLimit-Remaining"); got != "4" {
		t.Errorf("X-RateLimit-Remaining = %q, want 4", got)
	}
	if result.headers.Get("X-RateLimit-Reset") == "" {
		t.Error("X-RateLimit-Reset is missing")
	}
	if result.headers.Get("Retry-After") != "" {
		t.Error("the legacy response has no Retry-After header")
	}
}

func TestRegisterIssuesVerificationTokenWithoutAuthenticating(t *testing.T) {
	h := newHarness(t)
	emailAddress := "verify-" + uuid.NewString() + "@example.com"
	result := h.register(emailAddress)
	payload := result.decode(t)
	if strings.Contains(result.body, "accessToken") {
		t.Error("register must not authenticate")
	}

	var tokenCount int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM "EmailVerificationToken" WHERE "userId" = $1`, payload.Data.ID).Scan(&tokenCount); err != nil {
		t.Fatalf("count verification tokens: %v", err)
	}
	if tokenCount != 1 {
		t.Errorf("verification tokens = %d, want 1", tokenCount)
	}

	var stored string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "tokenHash" FROM "EmailVerificationToken" WHERE "userId" = $1`, payload.Data.ID).Scan(&stored); err != nil {
		t.Fatalf("read token hash: %v", err)
	}
	if len(stored) != 64 {
		t.Errorf("tokenHash = %q, want a sha256 hex digest", stored)
	}
}

func TestRegisterRejectsDuplicates(t *testing.T) {
	h := newHarness(t)
	emailAddress := "duplicate-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)

	result := h.post("/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, emailAddress, testPassword), "", "")
	if result.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", result.status, result.body)
	}
	payload := result.decode(t)
	if payload.Error.Code != "DUPLICATE_EMAIL" || payload.Error.Message != "An account with this email already exists." {
		t.Errorf("error = %+v", payload.Error)
	}
}

func TestRegisterNormalisesEmail(t *testing.T) {
	h := newHarness(t)
	result := h.post("/auth/register",
		fmt.Sprintf(`{"email":"  MiXeD-%s@Example.COM ","password":%q}`, uuid.NewString(), testPassword), "", "")
	if result.status != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", result.status, result.body)
	}
	payload := result.decode(t)
	if payload.Data.Email != strings.ToLower(strings.TrimSpace(payload.Data.Email)) {
		t.Errorf("email = %q, want it normalised", payload.Data.Email)
	}
}

func TestLoginAndRefreshRotation(t *testing.T) {
	h := newHarness(t)
	emailAddress := "rotate-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)

	login := h.login(emailAddress, testPassword)
	if login.status != http.StatusOK {
		t.Fatalf("login = %d %s", login.status, login.body)
	}
	payload := login.decode(t)
	if payload.Data.AccessToken == "" || payload.Data.RefreshToken == "" || payload.Data.User.ID == "" {
		t.Fatalf("login payload = %+v", payload.Data)
	}
	if wrong := h.login(emailAddress, "Wr0ng-Passw0rd1"); wrong.status != http.StatusUnauthorized ||
		!strings.Contains(wrong.body, `"code":"INVALID_CREDENTIALS"`) ||
		!strings.Contains(wrong.body, `"message":"Invalid credentials."`) {
		t.Errorf("wrong password = %d %s", wrong.status, wrong.body)
	}
	if unknown := h.login("nobody-"+uuid.NewString()+"@example.com", testPassword); unknown.status != http.StatusUnauthorized {
		t.Errorf("unknown user = %d %s", unknown.status, unknown.body)
	}

	rotated := h.post("/auth/refresh", fmt.Sprintf(`{"refreshToken":%q}`, payload.Data.RefreshToken), "", "")
	if rotated.status != http.StatusOK {
		t.Fatalf("refresh = %d %s", rotated.status, rotated.body)
	}
	next := rotated.decode(t)
	if next.Data.RefreshToken == payload.Data.RefreshToken {
		t.Error("the refresh token was not rotated")
	}
	if next.Data.AccessToken == "" {
		t.Error("refresh did not issue an access token")
	}

	var revokedAt *time.Time
	var replacedBy *string
	var familyID string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "revokedAt", "replacedByTokenId", "familyId" FROM "RefreshToken" WHERE "tokenHash" = $1`,
		digest(payload.Data.RefreshToken)).Scan(&revokedAt, &replacedBy, &familyID); err != nil {
		t.Fatalf("read rotated token: %v", err)
	}
	if revokedAt == nil || replacedBy == nil {
		t.Fatalf("the old token must be revoked and linked to its successor: %v/%v", revokedAt, replacedBy)
	}
	var successorFamily string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "familyId" FROM "RefreshToken" WHERE "id" = $1`, *replacedBy).Scan(&successorFamily); err != nil {
		t.Fatalf("read successor: %v", err)
	}
	if successorFamily != familyID {
		t.Errorf("family = %s/%s, want the rotation to stay in one family", familyID, successorFamily)
	}
	var lastUsedAt *time.Time
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "lastUsedAt" FROM "RefreshToken" WHERE "id" = $1`, *replacedBy).Scan(&lastUsedAt); err != nil {
		t.Fatalf("read successor lastUsedAt: %v", err)
	}
	if lastUsedAt == nil {
		t.Error("the successor must record lastUsedAt, like the legacy insert does")
	}
}

func TestRefreshReuseBurnsTheFamily(t *testing.T) {
	h := newHarness(t)
	emailAddress := "reuse-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)
	login := h.login(emailAddress, testPassword).decode(t)

	first := h.post("/auth/refresh", fmt.Sprintf(`{"refreshToken":%q}`, login.Data.RefreshToken), "", "")
	if first.status != http.StatusOK {
		t.Fatalf("refresh = %d %s", first.status, first.body)
	}
	rotated := first.decode(t)

	replay := h.post("/auth/refresh", fmt.Sprintf(`{"refreshToken":%q}`, login.Data.RefreshToken), "", "")
	if replay.status != http.StatusUnauthorized {
		t.Fatalf("replay = %d %s", replay.status, replay.body)
	}
	if !strings.Contains(replay.body, `"code":"REVOKED_TOKEN"`) ||
		!strings.Contains(replay.body, `"message":"Refresh token is no longer valid."`) {
		t.Errorf("replay body = %s", replay.body)
	}

	after := h.post("/auth/refresh", fmt.Sprintf(`{"refreshToken":%q}`, rotated.Data.RefreshToken), "", "")
	if after.status != http.StatusUnauthorized {
		t.Fatalf("the successor must be revoked by reuse detection, got %d: %s", after.status, after.body)
	}
}

func TestLogoutRevokesOnce(t *testing.T) {
	h := newHarness(t)
	emailAddress := "logout-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)
	login := h.login(emailAddress, testPassword).decode(t)

	first := h.post("/auth/logout", fmt.Sprintf(`{"refreshToken":%q}`, login.Data.RefreshToken), "", "")
	if first.status != http.StatusOK || !strings.Contains(first.body, `"revoked":true`) {
		t.Fatalf("logout = %d %s", first.status, first.body)
	}
	second := h.post("/auth/logout", fmt.Sprintf(`{"refreshToken":%q}`, login.Data.RefreshToken), "", "")
	if second.status != http.StatusOK || !strings.Contains(second.body, `"revoked":false`) {
		t.Fatalf("second logout = %d %s", second.status, second.body)
	}
	unknown := h.post("/auth/logout", fmt.Sprintf(`{"refreshToken":%q}`, strings.Repeat("a", 64)), "", "")
	if unknown.status != http.StatusOK || !strings.Contains(unknown.body, `"revoked":false`) {
		t.Fatalf("unknown token logout = %d %s", unknown.status, unknown.body)
	}
}

func TestEmailVerificationConfirm(t *testing.T) {
	h := newHarness(t)
	emailAddress := "confirm-" + uuid.NewString() + "@example.com"
	userID := h.register(emailAddress).decode(t).Data.ID
	raw := "v" + strings.Repeat("a", 47)
	if _, err := h.db.Pool.Exec(context.Background(), `
		INSERT INTO "EmailVerificationToken" ("id", "userId", "tokenHash", "expiresAt", "createdAt")
		VALUES ($1, $2, $3, now() + interval '1 day', now())`,
		uuid.NewString(), userID, digest(raw)); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	confirmed := h.post("/auth/email/verify/confirm", fmt.Sprintf(`{"token":%q}`, raw), "", "")
	if confirmed.status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", confirmed.status, confirmed.body)
	}
	payload := confirmed.decode(t)
	if payload.Data.VerificationStatus != "VERIFIED" || payload.Data.EmailVerifiedAt == nil {
		t.Errorf("payload = %+v", payload.Data)
	}

	replay := h.post("/auth/email/verify/confirm", fmt.Sprintf(`{"token":%q}`, raw), "", "")
	if replay.status != http.StatusBadRequest {
		t.Fatalf("replay = %d %s", replay.status, replay.body)
	}
	if !strings.Contains(replay.body, `"code":"EXPIRED_TOKEN"`) ||
		!strings.Contains(replay.body, `"message":"Verification token is invalid or has expired."`) {
		t.Errorf("replay body = %s", replay.body)
	}
}

func TestPasswordResetConfirmChangesPasswordAndRevokesSessions(t *testing.T) {
	h := newHarness(t)
	emailAddress := "reset-" + uuid.NewString() + "@example.com"
	userID := h.register(emailAddress).decode(t).Data.ID
	login := h.login(emailAddress, testPassword).decode(t)

	request := h.post("/auth/password/reset/request", fmt.Sprintf(`{"email":%q}`, emailAddress), "", "")
	if request.status != http.StatusOK || !strings.Contains(request.body, `"requested":true`) {
		t.Fatalf("request = %d %s", request.status, request.body)
	}
	unknown := h.post("/auth/password/reset/request",
		fmt.Sprintf(`{"email":%q}`, "nobody-"+uuid.NewString()+"@example.com"), "", "")
	if unknown.status != http.StatusOK || !strings.Contains(unknown.body, `"requested":true`) {
		t.Fatalf("unknown email must stay generic, got %d %s", unknown.status, unknown.body)
	}
	var tokenCount int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM "PasswordResetToken" WHERE "userId" = $1`, userID).Scan(&tokenCount); err != nil {
		t.Fatalf("count reset tokens: %v", err)
	}
	if tokenCount != 1 {
		t.Errorf("reset tokens = %d, want 1", tokenCount)
	}

	raw := "r" + strings.Repeat("b", 47)
	if _, err := h.db.Pool.Exec(context.Background(), `
		INSERT INTO "PasswordResetToken" ("id", "userId", "tokenHash", "expiresAt", "createdAt")
		VALUES ($1, $2, $3, now() + interval '1 day', now())`,
		uuid.NewString(), userID, digest(raw)); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	confirmed := h.post("/auth/password/reset/confirm",
		fmt.Sprintf(`{"token":%q,"newPassword":%q}`, raw, newPassword), "", "")
	if confirmed.status != http.StatusOK || !strings.Contains(confirmed.body, `"reset":true`) {
		t.Fatalf("confirm = %d %s", confirmed.status, confirmed.body)
	}
	if wrong := h.login(emailAddress, testPassword); wrong.status != http.StatusUnauthorized {
		t.Errorf("the old password must stop working: %d %s", wrong.status, wrong.body)
	}
	if fresh := h.login(emailAddress, newPassword); fresh.status != http.StatusOK {
		t.Errorf("the new password must work: %d %s", fresh.status, fresh.body)
	}
	if reused := h.post("/auth/refresh",
		fmt.Sprintf(`{"refreshToken":%q}`, login.Data.RefreshToken), "", ""); reused.status != http.StatusUnauthorized {
		t.Errorf("sessions must be revoked, got %d %s", reused.status, reused.body)
	}
	replay := h.post("/auth/password/reset/confirm",
		fmt.Sprintf(`{"token":%q,"newPassword":%q}`, raw, newPassword), "", "")
	if replay.status != http.StatusBadRequest || !strings.Contains(replay.body, "EXPIRED_TOKEN") {
		t.Errorf("replay = %d %s", replay.status, replay.body)
	}
}

func TestPasswordChangeRevokesSessions(t *testing.T) {
	h := newHarness(t)
	emailAddress := "change-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)
	login := h.login(emailAddress, testPassword).decode(t)

	wrong := h.post("/auth/password/change",
		`{"currentPassword":"Wr0ng-Passw0rd1","newPassword":"F1nal-Passw0rd!"}`, login.Data.AccessToken, "")
	if wrong.status != http.StatusUnauthorized || !strings.Contains(wrong.body, "Current password is incorrect.") {
		t.Fatalf("wrong current password = %d %s", wrong.status, wrong.body)
	}

	changed := h.post("/auth/password/change",
		`{"currentPassword":"`+testPassword+`","newPassword":"F1nal-Passw0rd!"}`, login.Data.AccessToken, "")
	if changed.status != http.StatusOK || !strings.Contains(changed.body, `"changed":true`) {
		t.Fatalf("change = %d %s", changed.status, changed.body)
	}
	if reused := h.post("/auth/refresh",
		fmt.Sprintf(`{"refreshToken":%q}`, login.Data.RefreshToken), "", ""); reused.status != http.StatusUnauthorized {
		t.Errorf("sessions must be revoked, got %d %s", reused.status, reused.body)
	}
	if fresh := h.login(emailAddress, "F1nal-Passw0rd!"); fresh.status != http.StatusOK {
		t.Errorf("the new password must work: %d %s", fresh.status, fresh.body)
	}
}

func TestPasswordChangeRequiresValidAccessToken(t *testing.T) {
	h := newHarness(t)
	emailAddress := "guard-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)
	body := `{"currentPassword":"` + testPassword + `","newPassword":"F1nal-Passw0rd!"}`

	missing := h.post("/auth/password/change", body, "", "")
	if missing.status != http.StatusUnauthorized || !strings.Contains(missing.body, "Authentication required.") {
		t.Errorf("missing token = %d %s", missing.status, missing.body)
	}
	if missing.headers.Get("X-RateLimit-Limit") != "" {
		t.Error("unauthenticated routes are not rate limited")
	}
	garbage := h.post("/auth/password/change", body, "not-a-jwt", "")
	if garbage.status != http.StatusUnauthorized ||
		!strings.Contains(garbage.body, "Invalid or expired access token.") ||
		!strings.Contains(garbage.body, "EXPIRED_TOKEN") {
		t.Errorf("garbage token = %d %s", garbage.status, garbage.body)
	}
}

func TestUsersMeRequiresAuthentication(t *testing.T) {
	h := newHarness(t)
	missing := h.get("/users/me", "")
	if missing.status != http.StatusUnauthorized || !strings.Contains(missing.body, "Authentication required.") {
		t.Errorf("missing token = %d %s", missing.status, missing.body)
	}
}

func TestUsersMeAndProfilePatch(t *testing.T) {
	h := newHarness(t)
	emailAddress := "profile-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)
	login := h.login(emailAddress, testPassword).decode(t)

	me := h.get("/users/me", login.Data.AccessToken)
	if me.status != http.StatusOK {
		t.Fatalf("me = %d %s", me.status, me.body)
	}
	if payload := me.decode(t); payload.Data.Email != emailAddress {
		t.Errorf("me email = %q", payload.Data.Email)
	}

	patched := h.patch("/users/me", `{"displayName":"  Ada  ","bio":"hello"}`, login.Data.AccessToken)
	if patched.status != http.StatusOK {
		t.Fatalf("patch = %d %s", patched.status, patched.body)
	}
	payload := patched.decode(t)
	if payload.Data.DisplayName == nil || *payload.Data.DisplayName != "Ada" {
		t.Errorf("displayName = %v", payload.Data.DisplayName)
	}
	if payload.Data.Bio == nil || *payload.Data.Bio != "hello" {
		t.Errorf("bio = %v", payload.Data.Bio)
	}

	whitelisted := h.patch("/users/me", `{"role":"STAFF"}`, login.Data.AccessToken)
	if whitelisted.status != http.StatusBadRequest ||
		!strings.Contains(whitelisted.body, "property role should not exist") {
		t.Errorf("whitelist = %d %s", whitelisted.status, whitelisted.body)
	}

	before := h.get("/users/me", login.Data.AccessToken)
	empty := h.patch("/users/me", `{}`, login.Data.AccessToken)
	if empty.status != http.StatusOK {
		t.Errorf("empty patch = %d %s", empty.status, empty.body)
	}
	if prior, after := before.decode(t).Data.UpdatedAt, empty.decode(t).Data.UpdatedAt; prior != after {
		t.Errorf("updatedAt moved for an empty patch: %s -> %s", prior, after)
	}
}

func TestRateLimitReturns429AfterTheWindowFills(t *testing.T) {
	h := newHarness(t)
	emailAddress := "limit-" + uuid.NewString() + "@example.com"
	for attempt := 0; attempt < 5; attempt++ {
		result := h.login(emailAddress, testPassword)
		if result.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d %s", attempt, result.status, result.body)
		}
		if got := result.headers.Get("X-RateLimit-Remaining"); got != fmt.Sprint(4-attempt) {
			t.Errorf("attempt %d remaining = %q", attempt, got)
		}
	}
	limited := h.login(emailAddress, testPassword)
	if limited.status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", limited.status)
	}
	if !strings.Contains(limited.body, `"code":"RATE_LIMITED"`) ||
		!strings.Contains(limited.body, `"message":"Too many requests. Please try again later."`) {
		t.Errorf("body = %s", limited.body)
	}
	if got := limited.headers.Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("remaining = %q, want 0", got)
	}
	if limited.headers.Get("Retry-After") != "" {
		t.Error("the legacy response has no Retry-After header")
	}
}

func TestMalformedJSONIsRejectedBeforeTheRateLimiter(t *testing.T) {
	h := newHarness(t)
	emailAddress := "malformed-" + uuid.NewString() + "@example.com"
	want := `{"success":false,"error":{"code":"VALIDATION_ERROR","message":"Unexpected end of JSON input"}}`
	for attempt := 0; attempt < 8; attempt++ {
		result := h.post("/auth/register", `{"email":`, "", "")
		if result.status != http.StatusBadRequest {
			t.Fatalf("attempt %d = %d %s", attempt, result.status, result.body)
		}
		if result.body != want {
			t.Fatalf("body = %s, want %s", result.body, want)
		}
		if got := result.headers.Get("X-RateLimit-Limit"); got != "" {
			t.Errorf("attempt %d reached the rate limiter: %q", attempt, got)
		}
	}
	fresh := h.post("/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, emailAddress, testPassword), "", "")
	if fresh.status != http.StatusCreated {
		t.Fatalf("the limiter must still have its full budget: %d %s", fresh.status, fresh.body)
	}
	if got := fresh.headers.Get("X-RateLimit-Remaining"); got != "4" {
		t.Errorf("remaining = %q, want 4", got)
	}
}

func TestFormEncodedBodyIsParsedAsAForm(t *testing.T) {
	h := newHarness(t)
	emailAddress := "form-" + uuid.NewString() + "@example.com"
	result := h.post("/auth/register", "email="+emailAddress+"&password="+testPassword, "",
		"application/x-www-form-urlencoded")
	if result.status != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", result.status, result.body)
	}
	if payload := result.decode(t); payload.Data.Email != emailAddress {
		t.Errorf("email = %q", payload.Data.Email)
	}

	fallback := "notype-" + uuid.NewString() + "@example.com"
	result = h.post("/auth/register", "email="+fallback+"&password="+testPassword, "", noContentType)
	if result.status != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", result.status, result.body)
	}
	if payload := result.decode(t); payload.Data.Email != fallback {
		t.Errorf("email = %q", payload.Data.Email)
	}
}

func TestPayloadTooLargeIsRejected(t *testing.T) {
	h := newHarness(t)
	body := `{"email":"` + strings.Repeat("x", 2*1024*1024) + `","password":"` + testPassword + `"}`
	result := h.post("/auth/register", body, "", "")
	if result.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, body = %s", result.status, result.body)
	}
	if !strings.Contains(result.body, `"code":"PAYLOAD_TOO_LARGE"`) {
		t.Errorf("body = %s", result.body)
	}
}

func TestConcurrentRotationsProduceExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	emailAddress := "concurrent-" + uuid.NewString() + "@example.com"
	h.register(emailAddress)
	login := h.login(emailAddress, testPassword).decode(t)

	const attempts = 6
	results := make(chan response, attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			results <- h.post("/auth/refresh", fmt.Sprintf(`{"refreshToken":%q}`, login.Data.RefreshToken), "", "")
		}()
	}
	winners := 0
	for i := 0; i < attempts; i++ {
		result := <-results
		switch result.status {
		case http.StatusOK:
			winners++
		case http.StatusUnauthorized:
		default:
			t.Errorf("unexpected status %d: %s", result.status, result.body)
		}
	}
	if winners != 1 {
		t.Errorf("winners = %d, want exactly 1", winners)
	}
}

func TestNonJSONContentTypeIsIgnored(t *testing.T) {
	h := newHarness(t)
	result := h.post("/auth/register", `{"email":"a@b.test","password":"`+testPassword+`"}`, "", "text/plain")
	if result.status != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", result.status, result.body)
	}
	payload := result.decode(t)
	if len(payload.Error.Details) != 6 {
		t.Fatalf("details = %v, want the six legacy messages", payload.Error.Details)
	}
	for _, want := range []string{
		"email must be shorter than or equal to 255 characters",
		"email must be a valid email address",
		"password must contain at least one lowercase letter, one uppercase letter, and one digit",
		"password must be shorter than or equal to 128 characters",
		"password must be at least 8 characters",
		"password must be a string",
	} {
		if !contains(payload.Error.Details, want) {
			t.Errorf("details %v are missing %q", payload.Error.Details, want)
		}
	}
	if payload.Error.Message != "Validation failed." || payload.Error.Code != "VALIDATION_ERROR" {
		t.Errorf("error = %+v", payload.Error)
	}
}

func TestUnknownRouteAndMethodAreNestStyle404(t *testing.T) {
	h := newHarness(t)
	request, err := http.NewRequest(http.MethodGet, h.http.URL+apiPrefix+"/auth/login", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	result, err := h.http.Client().Do(request)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = result.Body.Close() }()
	raw, _ := io.ReadAll(result.Body)
	if result.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", result.StatusCode, raw)
	}
	if !strings.Contains(string(raw), `"message":"Cannot GET /api/v1/auth/login"`) {
		t.Errorf("body = %s", raw)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
