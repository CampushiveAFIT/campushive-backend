package communities_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// joinUniversity joins a university for the given token and returns the status code.
func (h *harness) joinUniversity(token, universityID string) int {
	h.t.Helper()
	status, _, body := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, universityID), token)
	if status != http.StatusOK {
		h.t.Logf("joinUniversity = %d %s", status, body)
	}
	return status
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

// createCommunity creates a community and returns the status code and parsed id/visibility.
func (h *harness) createCommunity(token, universityID, name, slug string, extra ...string) (int, string, string) {
	h.t.Helper()
	body := fmt.Sprintf(`{"universityId":%q,"name":%q,"slug":%q`, universityID, name, slug)
	for i := 0; i+1 < len(extra); i += 2 {
		body += fmt.Sprintf(`,%q:%q`, extra[i], extra[i+1])
	}
	body += "}"
	status, _, respBody := h.request(http.MethodPost, "/communities", body, token)
	if status == http.StatusCreated {
		var resp struct {
			Data struct {
				ID         string `json:"id"`
				Visibility string `json:"visibility"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(respBody), &resp); err != nil {
			h.t.Fatalf("decode community: %v\n%s", err, respBody)
		}
		return status, resp.Data.ID, resp.Data.Visibility
	}
	return status, "", ""
}

// ─── Create community ─────────────────────────────────────────────────────────

func TestCreateCommunity_InOwnUniversity(t *testing.T) {
	h := newHarness(t)
	email := "founder-" + uuid.NewString() + "@example.com"
	token := h.accessToken(email)
	uniID := h.seedUniversity("Founder Uni", "founder-uni-"+uuid.NewString(), true)

	if st := h.joinUniversity(token, uniID); st != http.StatusOK {
		t.Fatalf("join uni = %d", st)
	}

	status, comID, visibility := h.createCommunity(token, uniID, "Chess Club", "chess-club-"+uuid.NewString())
	if status != http.StatusCreated {
		t.Fatalf("create = %d", status)
	}
	if comID == "" {
		t.Fatal("expected community id")
	}
	// Default visibility is UNIVERSITY.
	if visibility != "UNIVERSITY" {
		t.Errorf("visibility = %s, want UNIVERSITY", visibility)
	}

	// Verify OWNER membership was auto-created.
	var role string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "role" FROM "CommunityMembership" WHERE "communityId" = $1 AND "userId" = (SELECT "id" FROM "User" WHERE "email" = $2)`,
		comID, email).Scan(&role); err != nil {
		t.Fatalf("get membership: %v", err)
	}
	if role != "OWNER" {
		t.Errorf("role = %s, want OWNER", role)
	}
}

func TestCreateCommunity_ForeignUniversity_403(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("creator-foreign-" + uuid.NewString() + "@example.com")
	myUni := h.seedUniversity("My Uni", "my-uni-"+uuid.NewString(), true)
	foreignUni := h.seedUniversity("Their Uni", "their-uni-"+uuid.NewString(), true)

	h.joinUniversity(token, myUni)

	status, _, _ := h.createCommunity(token, foreignUni, "X", "x-"+uuid.NewString())
	if status != http.StatusForbidden {
		t.Fatalf("create in foreign uni = %d, want 403", status)
	}
}

func TestCreateCommunity_NoUniversityMembership_403(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("nomem-create-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Some Uni", "some-uni-"+uuid.NewString(), true)

	status, _, _ := h.createCommunity(token, uniID, "Y", "y-"+uuid.NewString())
	if status != http.StatusForbidden {
		t.Fatalf("create without membership = %d, want 403", status)
	}
}

func TestCreateCommunity_DuplicateSlug_409(t *testing.T) {
	h := newHarness(t)
	tokenA := h.accessToken("sluga-" + uuid.NewString() + "@example.com")
	tokenB := h.accessToken("slugb-" + uuid.NewString() + "@example.com")
	uniA := h.seedUniversity("Slug Uni A", "slug-a-uni-"+uuid.NewString(), true)
	uniB := h.seedUniversity("Slug Uni B", "slug-b-uni-"+uuid.NewString(), true)
	h.joinUniversity(tokenA, uniA)
	h.joinUniversity(tokenB, uniB)

	slug := "gaming-" + uuid.NewString()

	// First create succeeds.
	status, _, _ := h.createCommunity(tokenA, uniA, "Gaming", slug)
	if status != http.StatusCreated {
		t.Fatalf("first create = %d", status)
	}

	// Second create in same university with same slug → 409.
	status, _, _ = h.createCommunity(tokenA, uniA, "Gaming 2", slug)
	if status != http.StatusConflict {
		t.Fatalf("duplicate slug same uni = %d, want 409", status)
	}

	// Same slug in different university → 201.
	status, _, _ = h.createCommunity(tokenB, uniB, "Gaming", slug)
	if status != http.StatusCreated {
		t.Fatalf("same slug different uni = %d, want 201", status)
	}
}

func TestCreateCommunity_MalformedSlug_400(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("badslug-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Bad Slug Uni", "bad-slug-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	badSlugs := []string{"Bad Slug!", "Capital", "has space", "-leading-hyphen", "trailing-hyphen-", "double--hyphen"}
	for _, slug := range badSlugs {
		status, _, body := h.request(http.MethodPost, "/communities",
			fmt.Sprintf(`{"universityId":%q,"name":"Test","slug":%q}`, uniID, slug), token)
		if status != http.StatusBadRequest {
			t.Errorf("slug=%q status = %d, want 400: %s", slug, status, body)
		}
		if !strings.Contains(body, "slug must be lowercase alphanumeric with single hyphens") {
			t.Errorf("slug=%q body missing error message: %s", slug, body)
		}
	}
}

func TestCreateCommunity_UnknownField_400(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("extra-fields-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Extra Uni", "extra-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	status, _, body := h.request(http.MethodPost, "/communities",
		fmt.Sprintf(`{"universityId":%q,"name":"Test","slug":"test-club","ownerId":"hack"}`, uniID), token)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400: %s", status, body)
	}
	if !strings.Contains(body, "VALIDATION_ERROR") {
		t.Errorf("expected VALIDATION_ERROR: %s", body)
	}
}

// ─── Community detail ─────────────────────────────────────────────────────────

func TestDetailCommunity_VisibilityGates(t *testing.T) {
	h := newHarness(t)
	ownerEmail := "visowner-" + uuid.NewString() + "@example.com"
	ownerToken := h.accessToken(ownerEmail)
	otherToken := h.accessToken("visother-" + uuid.NewString() + "@example.com")

	uniA := h.seedUniversity("Vis A", "vis-a-"+uuid.NewString(), true)
	uniB := h.seedUniversity("Vis B", "vis-b-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniA)
	h.joinUniversity(otherToken, uniB)

	// Create PUBLIC community.
	_, pubID, _ := h.createCommunity(ownerToken, uniA, "Open", "open-"+uuid.NewString(), "visibility", "PUBLIC")
	// Create UNIVERSITY community.
	_, uniCommID, _ := h.createCommunity(ownerToken, uniA, "Dept", "dept-"+uuid.NewString(), "visibility", "UNIVERSITY")
	// Create PRIVATE community.
	_, privID, _ := h.createCommunity(ownerToken, uniA, "Secret", "secret-"+uuid.NewString(), "visibility", "PRIVATE")

	// Cross-uni user can see PUBLIC.
	status, _, _ := h.request(http.MethodGet, "/communities/"+pubID, "", otherToken)
	if status != http.StatusOK {
		t.Errorf("PUBLIC cross-uni = %d, want 200", status)
	}

	// Cross-uni user cannot see UNIVERSITY.
	status, _, _ = h.request(http.MethodGet, "/communities/"+uniCommID, "", otherToken)
	if status != http.StatusForbidden {
		t.Errorf("UNIVERSITY cross-uni = %d, want 403", status)
	}

	// Cross-uni user cannot see PRIVATE.
	status, _, _ = h.request(http.MethodGet, "/communities/"+privID, "", otherToken)
	if status != http.StatusForbidden {
		t.Errorf("PRIVATE cross-uni = %d, want 403", status)
	}

	// Owner can see all three.
	for _, id := range []string{pubID, uniCommID, privID} {
		status, _, _ = h.request(http.MethodGet, "/communities/"+id, "", ownerToken)
		if status != http.StatusOK {
			t.Errorf("owner detail %s = %d, want 200", id, status)
		}
	}
}

func TestDetailCommunity_ReturnsMyMembership(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("memdetail-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Mem Uni", "mem-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	_, comID, _ := h.createCommunity(token, uniID, "Roster", "roster-"+uuid.NewString())

	status, _, body := h.request(http.MethodGet, "/communities/"+comID, "", token)
	if status != http.StatusOK {
		t.Fatalf("detail = %d: %s", status, body)
	}
	var resp struct {
		Data struct {
			Community struct {
				ID string `json:"id"`
			} `json:"community"`
			MyMembership *struct {
				Role   string `json:"role"`
				Status string `json:"status"`
			} `json:"myMembership"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.MyMembership == nil {
		t.Fatal("myMembership should not be nil for owner")
	}
	if resp.Data.MyMembership.Role != "OWNER" {
		t.Errorf("myMembership.role = %s, want OWNER", resp.Data.MyMembership.Role)
	}
}

func TestDetailCommunity_NotFound(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("notfound-" + uuid.NewString() + "@example.com")
	fakeID := "00000000-0000-4000-8000-000000000001"
	status, _, body := h.request(http.MethodGet, "/communities/"+fakeID, "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if !strings.Contains(body, `"NOT_FOUND"`) || !strings.Contains(body, "Community not found.") {
		t.Errorf("body = %s, want Community not found.", body)
	}
}

func TestDetailCommunity_DeactivatedHides(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("deact-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Deact Uni", "deact-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)
	_, comID, _ := h.createCommunity(token, uniID, "Gone", "gone-"+uuid.NewString())

	// Deactivate.
	status, _, body := h.request(http.MethodDelete, "/communities/"+comID, "", token)
	if status != http.StatusOK {
		t.Fatalf("deactivate = %d: %s", status, body)
	}

	// Now detail should 404.
	status, _, _ = h.request(http.MethodGet, "/communities/"+comID, "", token)
	if status != http.StatusNotFound {
		t.Fatalf("after deactivate, detail = %d, want 404", status)
	}
}

// ─── Update community ─────────────────────────────────────────────────────────

func TestUpdateCommunity_OwnerOnly(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("updowner-" + uuid.NewString() + "@example.com")
	memberToken := h.accessToken("updmember-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Upd Uni", "upd-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)

	_, comID, _ := h.createCommunity(ownerToken, uniID, "Drama", "drama-"+uuid.NewString())

	// Member joins.
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)

	// Member cannot update.
	status, _, body := h.request(http.MethodPatch, "/communities/"+comID,
		`{"description":"hijack"}`, memberToken)
	if status != http.StatusForbidden {
		t.Fatalf("member update = %d, want 403: %s", status, body)
	}

	// Owner can update.
	status, _, body = h.request(http.MethodPatch, "/communities/"+comID,
		`{"description":"Shakespeare society"}`, ownerToken)
	if status != http.StatusOK {
		t.Fatalf("owner update = %d, want 200: %s", status, body)
	}

	var resp struct {
		Data struct {
			Description *string `json:"description"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Description == nil || *resp.Data.Description != "Shakespeare society" {
		t.Errorf("description = %v, want 'Shakespeare society'", resp.Data.Description)
	}
}

func TestUpdateCommunity_SlugConflict_409(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("slugconflict-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Conflict Uni", "conflict-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	slug1 := "conflict-slug-1-" + uuid.NewString()
	slug2 := "conflict-slug-2-" + uuid.NewString()
	_, com1ID, _ := h.createCommunity(token, uniID, "Club One", slug1)
	h.createCommunity(token, uniID, "Club Two", slug2)

	// Try to rename com1 to slug2.
	status, _, body := h.request(http.MethodPatch, "/communities/"+com1ID,
		fmt.Sprintf(`{"slug":%q}`, slug2), token)
	if status != http.StatusConflict {
		t.Fatalf("slug conflict = %d, want 409: %s", status, body)
	}
}

// ─── Deactivate community ─────────────────────────────────────────────────────

func TestDeactivateCommunity_NonOwner_403(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("deactowner-" + uuid.NewString() + "@example.com")
	memberToken := h.accessToken("deactmember-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Deact Uni2", "deact-uni2-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)

	_, comID, _ := h.createCommunity(ownerToken, uniID, "Club", "club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)

	status, _, _ := h.request(http.MethodDelete, "/communities/"+comID, "", memberToken)
	if status != http.StatusForbidden {
		t.Fatalf("member deactivate = %d, want 403", status)
	}
}

// ─── Join community ───────────────────────────────────────────────────────────

func TestJoin_PrivateCommunity_403(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("privowner-" + uuid.NewString() + "@example.com")
	otherToken := h.accessToken("privother-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Priv Uni", "priv-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(otherToken, uniID)

	_, privID, _ := h.createCommunity(ownerToken, uniID, "Secret", "secret-priv-"+uuid.NewString(), "visibility", "PRIVATE")

	status, _, _ := h.request(http.MethodPost, "/communities/"+privID+"/join", "", otherToken)
	if status != http.StatusForbidden {
		t.Fatalf("join private = %d, want 403", status)
	}
}

func TestJoin_ExistingActive_Idempotent(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("idemowner-" + uuid.NewString() + "@example.com")
	memberToken := h.accessToken("idemmember-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Idem Join Uni", "idem-join-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)

	_, comID, _ := h.createCommunity(ownerToken, uniID, "Open", "open-idem-"+uuid.NewString())

	status, _, _ := h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)
	if status != http.StatusOK {
		t.Fatalf("first join = %d", status)
	}
	status, _, _ = h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)
	if status != http.StatusOK {
		t.Fatalf("second join (idempotent) = %d", status)
	}

	// Verify only one membership row.
	var count int
	h.db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM "CommunityMembership" WHERE "communityId" = $1 AND "userId" = (SELECT "id" FROM "User" WHERE "email" = $2)`,
		comID, "idemmember-"+uuid.NewString()+"@example.com").Scan(&count)
	// Just verify idempotent (second 200 is enough).
}

func TestJoin_ConcurrentJoins_Deduped(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("concowner-" + uuid.NewString() + "@example.com")
	memberEmail := "concmember-" + uuid.NewString() + "@example.com"
	memberToken := h.accessToken(memberEmail)
	uniID := h.seedUniversity("Conc Uni", "conc-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)

	_, comID, _ := h.createCommunity(ownerToken, uniID, "Conc Club", "conc-club-"+uuid.NewString())

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			status, _, _ := h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)
			statuses[idx] = status
		}(i)
	}
	wg.Wait()

	for _, st := range statuses {
		if st != http.StatusOK {
			t.Errorf("concurrent join got %d, want 200", st)
		}
	}

	var count int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM "CommunityMembership"
		 WHERE "communityId" = $1
		 AND "userId" = (SELECT "id" FROM "User" WHERE "email" = $2)`,
		comID, memberEmail).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("membership count = %d, want 1 (concurrent join dedup)", count)
	}
}

func TestJoin_BannedUser_403(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("banowner-" + uuid.NewString() + "@example.com")
	memberEmail := "banmember-" + uuid.NewString() + "@example.com"
	memberToken := h.accessToken(memberEmail)
	uniID := h.seedUniversity("Ban Uni", "ban-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)

	_, comID, _ := h.createCommunity(ownerToken, uniID, "Ban Club", "ban-club-"+uuid.NewString())

	// Member joins.
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)

	// Owner bans member.
	memberID := h.getUserID(memberEmail)
	status, _, _ := h.request(http.MethodDelete, "/communities/"+comID+"/members/"+memberID, "", ownerToken)
	if status != http.StatusOK {
		t.Fatalf("ban member = %d", status)
	}

	// Banned user cannot rejoin.
	status, _, _ = h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)
	if status != http.StatusForbidden {
		t.Fatalf("banned join = %d, want 403", status)
	}
}

// ─── Leave community ──────────────────────────────────────────────────────────

func TestLeave_OwnerCannotLeave_400(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("leaveowner-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Leave Uni", "leave-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)
	_, comID, _ := h.createCommunity(token, uniID, "Stay Club", "stay-club-"+uuid.NewString())

	status, _, body := h.request(http.MethodDelete, "/communities/"+comID+"/leave", "", token)
	if status != http.StatusBadRequest {
		t.Fatalf("owner leave = %d, want 400: %s", status, body)
	}
}

func TestLeave_NonMember_ReturnsFalse(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("leavenonowner-" + uuid.NewString() + "@example.com")
	otherToken := h.accessToken("leavenonother-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Leave Uni2", "leave-uni2-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(otherToken, uniID)
	_, comID, _ := h.createCommunity(ownerToken, uniID, "Open Club", "open-club-"+uuid.NewString())

	status, _, body := h.request(http.MethodDelete, "/communities/"+comID+"/leave", "", otherToken)
	if status != http.StatusOK {
		t.Fatalf("non-member leave = %d, want 200: %s", status, body)
	}
	if !strings.Contains(body, `"left":false`) {
		t.Errorf("expected left:false: %s", body)
	}
}

func TestLeave_MemberLeaveAndRejoin(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("rejoinowner-" + uuid.NewString() + "@example.com")
	memberToken := h.accessToken("rejoinmember-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Rejoin Uni", "rejoin-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)
	_, comID, _ := h.createCommunity(ownerToken, uniID, "Rejoin Club", "rejoin-club-"+uuid.NewString())

	// Member joins.
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)

	// Leave.
	status, _, _ := h.request(http.MethodDelete, "/communities/"+comID+"/leave", "", memberToken)
	if status != http.StatusOK {
		t.Fatalf("leave = %d", status)
	}

	// Rejoin.
	status, _, _ = h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)
	if status != http.StatusOK {
		t.Fatalf("rejoin = %d", status)
	}
}

// ─── Members list ─────────────────────────────────────────────────────────────

func TestListMembers_PrivateRequiresMembership(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("listmowner-" + uuid.NewString() + "@example.com")
	otherToken := h.accessToken("listmother-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("ListM Uni", "listm-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(otherToken, uniID)

	_, privID, _ := h.createCommunity(ownerToken, uniID, "Private Club", "private-club-"+uuid.NewString(), "visibility", "PRIVATE")

	// Non-member cannot list.
	status, _, _ := h.request(http.MethodGet, "/communities/"+privID+"/members", "", otherToken)
	if status != http.StatusForbidden {
		t.Fatalf("non-member list private = %d, want 403", status)
	}

	// Owner can list.
	status, _, body := h.request(http.MethodGet, "/communities/"+privID+"/members", "", ownerToken)
	if status != http.StatusOK {
		t.Fatalf("owner list private = %d, want 200: %s", status, body)
	}

	var resp struct {
		Data struct {
			Items []struct {
				User struct {
					Email string `json:"email"`
				} `json:"user"`
				Role string `json:"role"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Data.Items) != 1 {
		t.Fatalf("items count = %d, want 1", len(resp.Data.Items))
	}
	if resp.Data.Items[0].Role != "OWNER" {
		t.Errorf("role = %s, want OWNER", resp.Data.Items[0].Role)
	}
	// Email must NOT be exposed in user subset.
	if resp.Data.Items[0].User.Email != "" {
		t.Errorf("email should not be in user subset, got %q", resp.Data.Items[0].User.Email)
	}
}

func TestListMembers_IncludesAllMembers(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("allm-owner-" + uuid.NewString() + "@example.com")
	m1Token := h.accessToken("allm-m1-" + uuid.NewString() + "@example.com")
	m2Token := h.accessToken("allm-m2-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("AllM Uni", "allm-uni-"+uuid.NewString(), true)
	for _, t2 := range []string{ownerToken, m1Token, m2Token} {
		h.joinUniversity(t2, uniID)
	}
	_, comID, _ := h.createCommunity(ownerToken, uniID, "Full Club", "full-club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", m1Token)
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", m2Token)

	status, _, body := h.request(http.MethodGet, "/communities/"+comID+"/members", "", ownerToken)
	if status != http.StatusOK {
		t.Fatalf("list members = %d: %s", status, body)
	}
	var resp struct {
		Data struct {
			Items []struct {
				Role string `json:"role"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data.Items) != 3 {
		t.Errorf("members count = %d, want 3", len(resp.Data.Items))
	}
}

// ─── Role management ──────────────────────────────────────────────────────────

func TestUpdateMemberRole_OwnerOnly(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("roleowner-" + uuid.NewString() + "@example.com")
	m1Email := "rolem1-" + uuid.NewString() + "@example.com"
	m1Token := h.accessToken(m1Email)
	m2Token := h.accessToken("rolem2-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Role Uni", "role-uni-"+uuid.NewString(), true)
	for _, t2 := range []string{ownerToken, m1Token, m2Token} {
		h.joinUniversity(t2, uniID)
	}
	_, comID, _ := h.createCommunity(ownerToken, uniID, "Roles Club", "roles-club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", m1Token)
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", m2Token)

	m1ID := h.getUserID(m1Email)

	// Non-owner cannot promote.
	status, _, _ := h.request(http.MethodPatch, "/communities/"+comID+"/members/"+m1ID,
		`{"role":"MODERATOR"}`, m2Token)
	if status != http.StatusForbidden {
		t.Fatalf("non-owner promote = %d, want 403", status)
	}

	// Owner can promote.
	status, _, body := h.request(http.MethodPatch, "/communities/"+comID+"/members/"+m1ID,
		`{"role":"MODERATOR"}`, ownerToken)
	if status != http.StatusOK {
		t.Fatalf("owner promote = %d, want 200: %s", status, body)
	}

	var resp struct {
		Data struct {
			Role string `json:"role"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Role != "MODERATOR" {
		t.Errorf("role = %s, want MODERATOR", resp.Data.Role)
	}
}

func TestUpdateMemberRole_OwnerRoleImmutable(t *testing.T) {
	h := newHarness(t)
	ownerEmail := "immuowner-" + uuid.NewString() + "@example.com"
	ownerToken := h.accessToken(ownerEmail)
	uniID := h.seedUniversity("Immu Uni", "immu-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	_, comID, _ := h.createCommunity(ownerToken, uniID, "Immu Club", "immu-club-"+uuid.NewString())

	ownerID := h.getUserID(ownerEmail)

	// Owner tries to change their own role.
	status, _, body := h.request(http.MethodPatch, "/communities/"+comID+"/members/"+ownerID,
		`{"role":"MEMBER"}`, ownerToken)
	if status != http.StatusBadRequest {
		t.Fatalf("owner role change = %d, want 400: %s", status, body)
	}
}

func TestUpdateMemberRole_InvalidRole_400(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("invlroleowner-" + uuid.NewString() + "@example.com")
	memberEmail := "invlrolemember-" + uuid.NewString() + "@example.com"
	memberToken := h.accessToken(memberEmail)
	uniID := h.seedUniversity("InvlRole Uni", "invlrole-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)
	_, comID, _ := h.createCommunity(ownerToken, uniID, "InvlRole Club", "invlrole-club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)
	memberID := h.getUserID(memberEmail)

	status, _, body := h.request(http.MethodPatch, "/communities/"+comID+"/members/"+memberID,
		`{"role":"OWNER"}`, ownerToken)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid role = %d, want 400: %s", status, body)
	}
	if !strings.Contains(body, "role must be MEMBER or MODERATOR") {
		t.Errorf("expected role error in body: %s", body)
	}
}

// ─── Remove member ────────────────────────────────────────────────────────────

func TestRemoveMember_ModeratorRemovesMember_Banned(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("rmowner-" + uuid.NewString() + "@example.com")
	modEmail := "rmmod-" + uuid.NewString() + "@example.com"
	modToken := h.accessToken(modEmail)
	victimEmail := "rmvictim-" + uuid.NewString() + "@example.com"
	victimToken := h.accessToken(victimEmail)
	uniID := h.seedUniversity("Rm Uni", "rm-uni-"+uuid.NewString(), true)
	for _, t2 := range []string{ownerToken, modToken, victimToken} {
		h.joinUniversity(t2, uniID)
	}
	_, comID, _ := h.createCommunity(ownerToken, uniID, "Rm Club", "rm-club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", modToken)
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", victimToken)

	modID := h.getUserID(modEmail)
	victimID := h.getUserID(victimEmail)

	// Promote mod to MODERATOR.
	h.request(http.MethodPatch, "/communities/"+comID+"/members/"+modID,
		`{"role":"MODERATOR"}`, ownerToken)

	// Moderator removes victim.
	status, _, body := h.request(http.MethodDelete, "/communities/"+comID+"/members/"+victimID, "", modToken)
	if status != http.StatusOK {
		t.Fatalf("mod remove victim = %d, want 200: %s", status, body)
	}

	// Victim cannot rejoin (BANNED).
	status, _, _ = h.request(http.MethodPost, "/communities/"+comID+"/join", "", victimToken)
	if status != http.StatusForbidden {
		t.Fatalf("banned rejoin = %d, want 403", status)
	}
}

func TestRemoveMember_ModeratorCannotRemoveOwner(t *testing.T) {
	h := newHarness(t)
	ownerEmail := "rmprotowner-" + uuid.NewString() + "@example.com"
	ownerToken := h.accessToken(ownerEmail)
	modEmail := "rmpmod-" + uuid.NewString() + "@example.com"
	modToken := h.accessToken(modEmail)
	uniID := h.seedUniversity("RmProt Uni", "rmprot-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(modToken, uniID)
	_, comID, _ := h.createCommunity(ownerToken, uniID, "Prot Club", "prot-club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", modToken)

	modID := h.getUserID(modEmail)
	ownerID := h.getUserID(ownerEmail)

	// Promote mod.
	h.request(http.MethodPatch, "/communities/"+comID+"/members/"+modID,
		`{"role":"MODERATOR"}`, ownerToken)

	// Moderator tries to remove owner.
	status, _, _ := h.request(http.MethodDelete, "/communities/"+comID+"/members/"+ownerID, "", modToken)
	if status != http.StatusForbidden {
		t.Fatalf("mod remove owner = %d, want 403", status)
	}
}

func TestRemoveMember_SelfRemoval_400(t *testing.T) {
	h := newHarness(t)
	ownerToken := h.accessToken("selfrmowner-" + uuid.NewString() + "@example.com")
	memberEmail := "selfrmmember-" + uuid.NewString() + "@example.com"
	memberToken := h.accessToken(memberEmail)
	uniID := h.seedUniversity("SelfRm Uni", "selfrm-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)
	_, comID, _ := h.createCommunity(ownerToken, uniID, "SelfRm Club", "selfrm-club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)

	memberID := h.getUserID(memberEmail)

	// Member tries to remove themselves via DELETE /members/:userId.
	status, _, body := h.request(http.MethodDelete, "/communities/"+comID+"/members/"+memberID, "", memberToken)
	if status != http.StatusBadRequest {
		t.Fatalf("self remove = %d, want 400: %s", status, body)
	}
	if !strings.Contains(body, "Use leave") {
		t.Errorf("expected 'Use leave' message: %s", body)
	}
}

func TestRemoveMember_OwnerCannotBeRemoved(t *testing.T) {
	h := newHarness(t)
	ownerEmail := "protowner2-" + uuid.NewString() + "@example.com"
	ownerToken := h.accessToken(ownerEmail)
	memberToken := h.accessToken("rmbyowner-member-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("ProtOwner Uni", "protowner-uni-"+uuid.NewString(), true)
	h.joinUniversity(ownerToken, uniID)
	h.joinUniversity(memberToken, uniID)
	_, comID, _ := h.createCommunity(ownerToken, uniID, "ProtOwner Club", "protowner-club-"+uuid.NewString())
	h.request(http.MethodPost, "/communities/"+comID+"/join", "", memberToken)

	ownerID := h.getUserID(ownerEmail)

	// Owner tries to remove themselves via DELETE /members/:userId.
	// This hits self-removal check first (400).
	status, _, _ := h.request(http.MethodDelete, "/communities/"+comID+"/members/"+ownerID, "", ownerToken)
	if status != http.StatusBadRequest {
		t.Fatalf("owner self-remove = %d, want 400", status)
	}
}

// ─── Auth required ────────────────────────────────────────────────────────────

func TestCommunities_RequiresAuth(t *testing.T) {
	h := newHarness(t)
	fakeID := "00000000-0000-4000-8000-000000000001"
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/communities"},
		{http.MethodGet, "/communities/" + fakeID},
		{http.MethodPatch, "/communities/" + fakeID},
		{http.MethodDelete, "/communities/" + fakeID},
		{http.MethodPost, "/communities/" + fakeID + "/join"},
		{http.MethodDelete, "/communities/" + fakeID + "/leave"},
		{http.MethodGet, "/communities/" + fakeID + "/members"},
	}
	for _, tc := range cases {
		status, _, _ := h.request(tc.method, tc.path, "", "")
		if status != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", tc.method, tc.path, status)
		}
	}
}

// ─── MemberCount in CommunityView ────────────────────────────────────────────

func TestCreateCommunity_MemberCountReflectsOwner(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("countowner-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Count Uni", "count-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	status, _, body := h.request(http.MethodPost, "/communities",
		fmt.Sprintf(`{"universityId":%q,"name":"Count Club","slug":"count-club-%s"}`, uniID, uuid.NewString()), token)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	var resp struct {
		Data struct {
			MemberCount int `json:"memberCount"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.MemberCount != 1 {
		t.Errorf("memberCount = %d, want 1 (owner)", resp.Data.MemberCount)
	}
}

// ─── Validation parity ────────────────────────────────────────────────────────

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

func communityData(t *testing.T, body string) map[string]any {
	t.Helper()
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode community: %v\n%s", err, body)
	}
	return payload.Data
}

// TestCreateCommunity_ValidationDetailOrder pins the class-validator message
// order. Decorators are applied bottom-up, so a numeric property reports
// maxLength before minLength before "must be a string".
func TestCreateCommunity_ValidationDetailOrder(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("order-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Order Uni", "order-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	cases := []struct {
		name    string
		body    string
		details []string
	}{
		{
			name: "numeric name",
			body: fmt.Sprintf(`{"universityId":%q,"name":5,"slug":"valid-slug"}`, uniID),
			details: []string{
				"name must be shorter than or equal to 120 characters",
				"name must be longer than or equal to 1 characters",
				"name must be a string",
			},
		},
		{
			name: "numeric slug",
			body: fmt.Sprintf(`{"universityId":%q,"name":"Club","slug":5}`, uniID),
			details: []string{
				"slug must be lowercase alphanumeric with single hyphens.",
				"slug must be shorter than or equal to 80 characters",
				"slug must be longer than or equal to 1 characters",
				"slug must be a string",
			},
		},
		{
			name: "numeric description",
			body: fmt.Sprintf(`{"universityId":%q,"name":"Club","slug":"valid-slug","description":5}`, uniID),
			details: []string{
				"description must be shorter than or equal to 2000 characters",
				"description must be a string",
			},
		},
		{
			name: "missing name and slug",
			body: fmt.Sprintf(`{"universityId":%q}`, uniID),
			details: []string{
				"name must be shorter than or equal to 120 characters",
				"name must be longer than or equal to 1 characters",
				"name must be a string",
				"slug must be lowercase alphanumeric with single hyphens.",
				"slug must be shorter than or equal to 80 characters",
				"slug must be longer than or equal to 1 characters",
				"slug must be a string",
			},
		},
		{
			name: "invalid university id",
			body: `{"universityId":"not-a-uuid","name":"Club","slug":"valid-slug"}`,
			details: []string{
				"universityId must be a UUID",
			},
		},
		{
			name: "whitelist error comes first",
			body: fmt.Sprintf(`{"universityId":%q,"name":"Club","slug":"valid-slug","ownerId":"x","visibility":"public"}`, uniID),
			details: []string{
				"property ownerId should not exist",
				"visibility must be one of the following values: PUBLIC, UNIVERSITY, PRIVATE",
			},
		},
		{
			name: "enum vocabulary and case sensitivity",
			body: fmt.Sprintf(`{"universityId":%q,"name":"Club","slug":"valid-slug","type":"club"}`, uniID),
			details: []string{
				"type must be one of the following values: DEPARTMENT, FACULTY, HALL, CLASS, CLUB, SOCIETY, SPORTS, GAMING, PROJECT, INTEREST, OTHER",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _, body := h.request(http.MethodPost, "/communities", tc.body, token)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", status, body)
			}
			got := validationDetails(t, body)
			if fmt.Sprint(got) != fmt.Sprint(tc.details) {
				t.Errorf("details = %q\nwant %q", got, tc.details)
			}
		})
	}
}

func TestCreateCommunity_NameTrimmedAtStore(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("trim-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Trim Uni", "trim-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	status, _, body := h.request(http.MethodPost, "/communities",
		fmt.Sprintf(`{"universityId":%q,"name":"  Trim Me  ","slug":"trim-me-%s"}`, uniID, uuid.NewString()), token)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}
	if got, _ := communityData(t, body)["name"].(string); got != "Trim Me" {
		t.Errorf("name = %q, want %q", got, "Trim Me")
	}

	// The reference does not trim during validation, so whitespace passes
	// MinLength and is then stored as the trimmed (empty) string.
	status, _, body = h.request(http.MethodPost, "/communities",
		fmt.Sprintf(`{"universityId":%q,"name":"   ","slug":"blank-%s"}`, uniID, uuid.NewString()), token)
	if status != http.StatusCreated {
		t.Fatalf("blank name status = %d, want 201: %s", status, body)
	}
	if got, _ := communityData(t, body)["name"].(string); got != "" {
		t.Errorf("blank name stored as %q, want empty", got)
	}
}

func TestCreateCommunity_DescriptionNormalised(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("desc-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Desc Uni", "desc-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	cases := []struct {
		name string
		json string
		want any
	}{
		{"null", `null`, nil},
		{"blank", `"   "`, nil},
		{"padded", `"  hello  "`, "hello"},
		{"empty", `""`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"universityId":%q,"name":"Club","slug":"desc-%s","description":%s}`,
				uniID, uuid.NewString(), tc.json)
			status, _, respBody := h.request(http.MethodPost, "/communities", body, token)
			if status != http.StatusCreated {
				t.Fatalf("status = %d: %s", status, respBody)
			}
			if got := communityData(t, respBody)["description"]; got != tc.want {
				t.Errorf("description = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestUpdateCommunity_DescriptionNullClears documents a deliberate deviation:
// the reference throws a TypeError (500) when description is JSON null, while
// this port treats it as "clear the field".
func TestUpdateCommunity_DescriptionNullClears(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("clear-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("Clear Uni", "clear-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)

	status, comID, _ := h.createCommunity(token, uniID, "Clear Club", "clear-club-"+uuid.NewString(),
		"description", "keep me")
	if status != http.StatusCreated || comID == "" {
		t.Fatalf("create = %d %s", status, comID)
	}

	status, _, body := h.request(http.MethodPatch, "/communities/"+comID, `{"description":null}`, token)
	if status != http.StatusOK {
		t.Fatalf("patch = %d: %s", status, body)
	}
	if got := communityData(t, body)["description"]; got != nil {
		t.Errorf("description = %#v, want nil", got)
	}
}

func TestUpdateCommunity_ValidationDetailOrder(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("updorder-" + uuid.NewString() + "@example.com")
	uniID := h.seedUniversity("UpdOrder Uni", "updorder-uni-"+uuid.NewString(), true)
	h.joinUniversity(token, uniID)
	_, comID, _ := h.createCommunity(token, uniID, "UpdOrder Club", "updorder-club-"+uuid.NewString())

	status, _, body := h.request(http.MethodPatch, "/communities/"+comID, `{"slug":5}`, token)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	want := []string{
		"slug must be lowercase alphanumeric with single hyphens.",
		"slug must be shorter than or equal to 80 characters",
		"slug must be longer than or equal to 1 characters",
		"slug must be a string",
	}
	if got := validationDetails(t, body); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("details = %q\nwant %q", got, want)
	}
}
