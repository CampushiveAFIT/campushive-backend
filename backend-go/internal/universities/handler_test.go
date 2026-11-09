package universities_test

import (
	"context"
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
	apiPrefix    = "/api/v1"
)

// ─── Test harness ─────────────────────────────────────────────────────────────

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
	req, err := http.NewRequest(method, h.http.URL+apiPrefix+path, reader)
	if err != nil {
		h.t.Fatalf("build request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.http.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(raw)
}

// accessToken registers and logs in a fresh user, returning its access token.
func (h *harness) accessToken(email string) string {
	h.t.Helper()
	status, _, body := h.request(http.MethodPost, "/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, testPassword), "")
	if status != http.StatusCreated {
		h.t.Fatalf("register = %d %s", status, body)
	}
	status, _, body = h.request(http.MethodPost, "/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, testPassword), "")
	if status != http.StatusOK {
		h.t.Fatalf("login = %d %s", status, body)
	}
	var payload struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		h.t.Fatalf("decode login: %v", err)
	}
	return payload.Data.AccessToken
}

// seedUniversity inserts a University row and returns its id.
func (h *harness) seedUniversity(name, slug string, isActive bool) string {
	h.t.Helper()
	id := uuid.NewString()
	now := time.Now()
	if _, err := h.db.Pool.Exec(context.Background(), `
		INSERT INTO "University" ("id", "name", "slug", "isActive", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, $5, $5)`,
		id, name, slug, isActive, now); err != nil {
		h.t.Fatalf("seed university %s: %v", slug, err)
	}
	return id
}

// seedCommunity inserts a Community row and returns its id.
func (h *harness) seedCommunity(universityID, name, slug string, visibility string) string {
	h.t.Helper()
	id := uuid.NewString()
	now := time.Now()
	if _, err := h.db.Pool.Exec(context.Background(), `
		INSERT INTO "Community" ("id", "universityId", "name", "slug", "type", "visibility", "isActive", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, 'CLUB', $5, true, $6, $6)`,
		id, universityID, name, slug, visibility, now); err != nil {
		h.t.Fatalf("seed community %s: %v", slug, err)
	}
	return id
}

// getUserID returns the user id for a given email address.
func (h *harness) getUserID(email string) string {
	h.t.Helper()
	var id string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "id" FROM "User" WHERE "email" = $1`, email).Scan(&id); err != nil {
		h.t.Fatalf("get user id for %s: %v", email, err)
	}
	return id
}

// membershipStatus returns the status of a user's membership for a university.
func (h *harness) membershipStatus(userID, universityID string) string {
	h.t.Helper()
	var status string
	err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "status" FROM "UniversityMembership" WHERE "userId" = $1 AND "universityId" = $2`,
		userID, universityID).Scan(&status)
	if err != nil {
		return ""
	}
	return status
}

// ─── List universities ────────────────────────────────────────────────────────

func TestListUniversities_RequiresAuth(t *testing.T) {
	h := newHarness(t)
	status, _, body := h.request(http.MethodGet, "/universities", "", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", status, body)
	}
}

func TestListUniversities_OnlyActiveShown(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("list-active-" + uuid.NewString() + "@example.com")

	activeID := h.seedUniversity("Active Uni", "active-uni-"+uuid.NewString(), true)
	_ = h.seedUniversity("Inactive Uni", "inactive-uni-"+uuid.NewString(), false)

	status, _, body := h.request(http.MethodGet, "/universities", "", token)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	var resp struct {
		Data struct {
			Items []struct {
				ID       string `json:"id"`
				IsActive bool   `json:"isActive"`
			} `json:"items"`
			NextCursor string `json:"nextCursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	for _, item := range resp.Data.Items {
		if !item.IsActive {
			t.Errorf("inactive university returned: %s", item.ID)
		}
	}
	found := false
	for _, item := range resp.Data.Items {
		if item.ID == activeID {
			found = true
		}
	}
	if !found {
		t.Errorf("active university %s not in list", activeID)
	}
}

func TestListUniversities_Pagination(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("paging-" + uuid.NewString() + "@example.com")

	suffix := uuid.NewString()
	for i := 0; i < 3; i++ {
		h.seedUniversity(fmt.Sprintf("Uni %d", i), fmt.Sprintf("uni-%d-%s", i, suffix), true)
		time.Sleep(2 * time.Millisecond) // ensure distinct createdAt
	}

	// First page: limit=2
	status, _, body := h.request(http.MethodGet, "/universities?limit=2", "", token)
	if status != http.StatusOK {
		t.Fatalf("page1 status = %d: %s", status, body)
	}
	var page1 struct {
		Data struct {
			Items      []struct{ ID string }
			NextCursor string `json:"nextCursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &page1); err != nil {
		t.Fatalf("decode page1: %v", err)
	}
	if len(page1.Data.Items) != 2 {
		t.Fatalf("page1 items = %d, want 2", len(page1.Data.Items))
	}
	if page1.Data.NextCursor == "" {
		t.Fatal("expected nextCursor after page 1")
	}

	// Second page
	status, _, body = h.request(http.MethodGet, "/universities?limit=2&cursor="+page1.Data.NextCursor, "", token)
	if status != http.StatusOK {
		t.Fatalf("page2 status = %d: %s", status, body)
	}
	var page2 struct {
		Data struct {
			Items []struct{ ID string }
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2.Data.Items) == 0 {
		t.Fatal("expected items on page 2")
	}
}

// ─── University detail ────────────────────────────────────────────────────────

func TestDetailUniversity_HappyPath(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("detail-" + uuid.NewString() + "@example.com")
	slug := "detail-uni-" + uuid.NewString()
	uniID := h.seedUniversity("Detail Uni", slug, true)

	// Seed 2 active memberships (distinct users).
	for i := 0; i < 2; i++ {
		mID := uuid.NewString()
		uID := uuid.NewString()
		now := time.Now()
		if _, err := h.db.Pool.Exec(context.Background(), `
			INSERT INTO "User" ("id", "email", "passwordHash", "role", "verificationStatus", "createdAt", "updatedAt")
			VALUES ($1, $2, 'hash', 'STUDENT', 'UNVERIFIED', $3, $3)`,
			uID, fmt.Sprintf("mem%d-%s@example.com", i, uuid.NewString()), now); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if _, err := h.db.Pool.Exec(context.Background(), `
			INSERT INTO "UniversityMembership" ("id", "userId", "universityId", "status", "role", "verificationStatus", "createdAt", "updatedAt")
			VALUES ($1, $2, $3, 'ACTIVE', 'STUDENT', 'UNVERIFIED', $4, $4)`,
			mID, uID, uniID, now); err != nil {
			t.Fatalf("seed membership: %v", err)
		}
	}
	// Seed 1 community.
	h.seedCommunity(uniID, "General", "general-"+uuid.NewString(), "PUBLIC")

	status, _, body := h.request(http.MethodGet, "/universities/"+slug, "", token)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var resp struct {
		Data struct {
			ID             string `json:"id"`
			MemberCount    int    `json:"memberCount"`
			CommunityCount int    `json:"communityCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.ID != uniID {
		t.Errorf("id = %s, want %s", resp.Data.ID, uniID)
	}
	if resp.Data.MemberCount != 2 {
		t.Errorf("memberCount = %d, want 2", resp.Data.MemberCount)
	}
	if resp.Data.CommunityCount != 1 {
		t.Errorf("communityCount = %d, want 1", resp.Data.CommunityCount)
	}
}

func TestDetailUniversity_NotFoundForMissingSlug(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("detail-miss-" + uuid.NewString() + "@example.com")
	status, _, body := h.request(http.MethodGet, "/universities/does-not-exist", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", status, body)
	}
	if !strings.Contains(body, `"NOT_FOUND"`) {
		t.Errorf("body = %s", body)
	}
	if !strings.Contains(body, "University not found.") {
		t.Errorf("body = %s, want University not found.", body)
	}
}

func TestDetailUniversity_NotFoundForInactiveSlug(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("detail-inactive-" + uuid.NewString() + "@example.com")
	slug := "inactive-" + uuid.NewString()
	h.seedUniversity("Inactive", slug, false)

	status, _, body := h.request(http.MethodGet, "/universities/"+slug, "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", status, body)
	}
}

// ─── Campus identity (GET /users/me/university) ───────────────────────────────

func TestMyUniversity_NoMembership(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("nomem-" + uuid.NewString() + "@example.com")
	status, _, body := h.request(http.MethodGet, "/users/me/university", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", status, body)
	}
	if !strings.Contains(body, `"NOT_FOUND"`) {
		t.Errorf("body = %s", body)
	}
	if !strings.Contains(body, "No active university membership.") {
		t.Errorf("body = %s, want No active university membership.", body)
	}
}

// ─── Campus identity (POST/PATCH /users/me/university) ───────────────────────

func TestJoinUniversity_CreatesActiveMembership(t *testing.T) {
	h := newHarness(t)
	email := "join-" + uuid.NewString() + "@example.com"
	token := h.accessToken(email)
	uniID := h.seedUniversity("Test Uni", "test-uni-"+uuid.NewString(), true)

	status, _, body := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uniID), token)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	var resp struct {
		Data struct {
			ID                 string `json:"id"`
			Status             string `json:"status"`
			Role               string `json:"role"`
			VerificationStatus string `json:"verificationStatus"`
			University         struct {
				ID string `json:"id"`
			} `json:"university"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if resp.Data.Status != "ACTIVE" {
		t.Errorf("status = %s, want ACTIVE", resp.Data.Status)
	}
	if resp.Data.Role != "STUDENT" {
		t.Errorf("role = %s, want STUDENT", resp.Data.Role)
	}
	if resp.Data.VerificationStatus != "UNVERIFIED" {
		t.Errorf("verificationStatus = %s, want UNVERIFIED", resp.Data.VerificationStatus)
	}
	if resp.Data.University.ID != uniID {
		t.Errorf("university.id = %s, want %s", resp.Data.University.ID, uniID)
	}

	// Confirm with GET.
	status, _, body = h.request(http.MethodGet, "/users/me/university", "", token)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d: %s", status, body)
	}
}

func TestJoinUniversity_IdempotentDuplicate(t *testing.T) {
	h := newHarness(t)
	email := "idem-" + uuid.NewString() + "@example.com"
	token := h.accessToken(email)
	uniID := h.seedUniversity("Idem Uni", "idem-uni-"+uuid.NewString(), true)

	status, _, body := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uniID), token)
	if status != http.StatusOK {
		t.Fatalf("first join = %d: %s", status, body)
	}

	// Second identical request must also succeed (idempotent).
	status, _, body = h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uniID), token)
	if status != http.StatusOK {
		t.Fatalf("second join = %d: %s", status, body)
	}
}

func TestJoinUniversity_InactiveUni404(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("inactive-join-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Inactive Uni", "inactive-join-"+uuid.NewString(), false)

	status, _, body := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uniID), token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", status, body)
	}
}

func TestJoinUniversity_SwitchSuspendsPrior(t *testing.T) {
	h := newHarness(t)
	email := "switch-" + uuid.NewString() + "@example.com"
	token := h.accessToken(email)
	userID := h.getUserID(email)

	uni1ID := h.seedUniversity("Uni One", "uni-one-"+uuid.NewString(), true)
	uni2ID := h.seedUniversity("Uni Two", "uni-two-"+uuid.NewString(), true)

	// Join first university.
	status, _, body := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uni1ID), token)
	if status != http.StatusOK {
		t.Fatalf("join uni1 = %d: %s", status, body)
	}

	// Switch to second university.
	status, _, body = h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uni2ID), token)
	if status != http.StatusOK {
		t.Fatalf("join uni2 = %d: %s", status, body)
	}

	// The first membership must now be SUSPENDED.
	if st := h.membershipStatus(userID, uni1ID); st != "SUSPENDED" {
		t.Errorf("uni1 membership status = %s, want SUSPENDED", st)
	}
	// The second membership must be ACTIVE.
	if st := h.membershipStatus(userID, uni2ID); st != "ACTIVE" {
		t.Errorf("uni2 membership status = %s, want ACTIVE", st)
	}
}

func TestJoinUniversity_PatchAndPostSameLogic(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("patch-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Patch Uni", "patch-uni-"+uuid.NewString(), true)

	// PATCH should work identically to POST.
	status, _, body := h.request(http.MethodPatch, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uniID), token)
	if status != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", status, body)
	}
}

func TestJoinUniversity_ForbiddenFields(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("forbidden-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Forbidden Uni", "forbidden-uni-"+uuid.NewString(), true)

	cases := []struct {
		name       string
		body       string
		wantInBody string
	}{
		{
			name:       "role field rejected",
			body:       fmt.Sprintf(`{"universityId":%q,"role":"ADMIN"}`, uniID),
			wantInBody: "property role should not exist",
		},
		{
			name:       "status field rejected",
			body:       fmt.Sprintf(`{"universityId":%q,"status":"ACTIVE"}`, uniID),
			wantInBody: "property status should not exist",
		},
		{
			name:       "verificationStatus field rejected",
			body:       fmt.Sprintf(`{"universityId":%q,"verificationStatus":"VERIFIED"}`, uniID),
			wantInBody: "property verificationStatus should not exist",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _, body := h.request(http.MethodPost, "/users/me/university", tc.body, token)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d: %s", status, body)
			}
			if !strings.Contains(body, `"VALIDATION_ERROR"`) {
				t.Errorf("want VALIDATION_ERROR in body: %s", body)
			}
			if !strings.Contains(body, tc.wantInBody) {
				t.Errorf("want %q in body: %s", tc.wantInBody, body)
			}
		})
	}
}

func validationDetails(t *testing.T, body string) []string {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string   `json:"code"`
			Details []string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode validation error: %v\n%s", err, body)
	}
	if payload.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("code = %q, want VALIDATION_ERROR: %s", payload.Error.Code, body)
	}
	return payload.Error.Details
}

// JoinUniversityDto declares a single @IsUUID('4') with a custom message, so
// every failure (wrong version, non-string, absent) reports that one message.
func TestJoinUniversity_InvalidUUID(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("baduuid-" + uuid.NewString() + "@example.com")

	cases := []string{
		`{"universityId":"not-a-uuid"}`,
		`{"universityId":"12345678-1234-1234-1234-123456789012"}`, // v1, not v4
		`{"universityId":"12345678-1234-4234-c234-123456789012"}`, // bad variant
		`{"universityId":123}`,
		`{}`,
	}
	for _, body := range cases {
		status, _, respBody := h.request(http.MethodPost, "/users/me/university", body, token)
		if status != http.StatusBadRequest {
			t.Errorf("body=%s status = %d, want 400: %s", body, status, respBody)
		}
		want := []string{"universityId must be a valid UUID."}
		if got := validationDetails(t, respBody); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("body=%s details = %q, want %q", body, got, want)
		}
	}
}

func TestJoinUniversity_RequiresAuth(t *testing.T) {
	h := newHarness(t)
	uniID := uuid.NewString()
	status, _, _ := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uniID), "")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}

// ─── Communities listing ──────────────────────────────────────────────────────

func TestListCommunities_PublicVisibleToNonMembers(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("comm-public-" + uuid.NewString() + "@example.com")
	slug := "comm-uni-" + uuid.NewString()
	uniID := h.seedUniversity("Comm Uni", slug, true)

	pubID := h.seedCommunity(uniID, "Public Club", "pub-"+uuid.NewString(), "PUBLIC")
	_ = h.seedCommunity(uniID, "Uni Club", "uni-"+uuid.NewString(), "UNIVERSITY")
	_ = h.seedCommunity(uniID, "Private Club", "priv-"+uuid.NewString(), "PRIVATE")

	status, _, body := h.request(http.MethodGet, "/universities/"+slug+"/communities", "", token)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var resp struct {
		Data struct {
			Items []struct {
				ID         string `json:"id"`
				Visibility string `json:"visibility"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, item := range resp.Data.Items {
		if item.Visibility != "PUBLIC" {
			t.Errorf("non-member sees non-public community %s (visibility=%s)", item.ID, item.Visibility)
		}
	}
	found := false
	for _, item := range resp.Data.Items {
		if item.ID == pubID {
			found = true
		}
	}
	if !found {
		t.Errorf("public community %s not in list", pubID)
	}
}

func TestListCommunities_UniversityVisibleToMembers(t *testing.T) {
	h := newHarness(t)
	email := "comm-mem-" + uuid.NewString() + "@example.com"
	token := h.accessToken(email)
	slug := "comm-uni2-" + uuid.NewString()
	uniID := h.seedUniversity("Comm Uni 2", slug, true)

	// Join the university so the caller is a member.
	status, _, body := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, uniID), token)
	if status != http.StatusOK {
		t.Fatalf("join uni = %d: %s", status, body)
	}

	_ = h.seedCommunity(uniID, "Public Club", "pub2-"+uuid.NewString(), "PUBLIC")
	uniClubID := h.seedCommunity(uniID, "Uni Club", "uni2-"+uuid.NewString(), "UNIVERSITY")
	_ = h.seedCommunity(uniID, "Private Club", "priv2-"+uuid.NewString(), "PRIVATE")

	status, _, body = h.request(http.MethodGet, "/universities/"+slug+"/communities", "", token)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var resp struct {
		Data struct {
			Items []struct {
				ID         string `json:"id"`
				Visibility string `json:"visibility"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	visibilities := map[string]string{}
	for _, item := range resp.Data.Items {
		visibilities[item.ID] = item.Visibility
	}
	if _, ok := visibilities[uniClubID]; !ok {
		t.Errorf("UNIVERSITY community %s not visible to member", uniClubID)
	}
}

func TestListCommunities_NotFoundForMissingSlug(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("comm-miss-" + uuid.NewString() + "@example.com")
	status, _, body := h.request(http.MethodGet, "/universities/does-not-exist/communities", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d: %s", status, body)
	}
}
