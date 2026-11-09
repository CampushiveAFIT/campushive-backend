package graph_test

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

func (h *harness) request(method, path, body, token string) (int, string) {
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
	return resp.StatusCode, string(raw)
}

// register creates a fresh active user and returns its access token and ID.
func (h *harness) register(label string) (string, string) {
	h.t.Helper()
	email := fmt.Sprintf("%s-%s@example.com", label, uuid.NewString())
	status, body := h.request(http.MethodPost, "/auth/register",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, testPassword), "")
	if status != http.StatusCreated {
		h.t.Fatalf("register = %d %s", status, body)
	}
	status, body = h.request(http.MethodPost, "/auth/login",
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
	return payload.Data.AccessToken, h.userID(email)
}

func (h *harness) userID(email string) string {
	h.t.Helper()
	var id string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "id" FROM "User" WHERE "email" = $1`, email).Scan(&id); err != nil {
		h.t.Fatalf("lookup user %s: %v", email, err)
	}
	return id
}

// followRowCount counts follow edges from follower to following.
func (h *harness) followRowCount(followerID, followingID string) int {
	h.t.Helper()
	var count int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM "Follow" WHERE "followerId" = $1 AND "followingId" = $2`,
		followerID, followingID).Scan(&count); err != nil {
		h.t.Fatalf("count follow: %v", err)
	}
	return count
}

func (h *harness) setFollowCreatedAt(followerID, followingID string, at time.Time) {
	h.t.Helper()
	if _, err := h.db.Pool.Exec(context.Background(),
		`UPDATE "Follow" SET "createdAt" = $1 WHERE "followerId" = $2 AND "followingId" = $3`,
		at, followerID, followingID); err != nil {
		h.t.Fatalf("set follow createdAt: %v", err)
	}
}

func (h *harness) outboxCount(eventType string) int {
	h.t.Helper()
	var count int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM "DomainEvent" WHERE "type" = $1`, eventType).Scan(&count); err != nil {
		h.t.Fatalf("count outbox: %v", err)
	}
	return count
}

func decodeData(t *testing.T, body string) map[string]any {
	t.Helper()
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode data: %v\n%s", err, body)
	}
	return payload.Data
}

type pageBody struct {
	Items      []map[string]any `json:"items"`
	NextCursor *string          `json:"nextCursor"`
}

func decodePage(t *testing.T, body string) pageBody {
	t.Helper()
	var payload struct {
		Data pageBody `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode page: %v\n%s", err, body)
	}
	return payload.Data
}

func errorCodeAndMessage(t *testing.T, body string) (string, string) {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode error: %v\n%s", err, body)
	}
	return payload.Error.Code, payload.Error.Message
}

func itemUserID(t *testing.T, item map[string]any) string {
	t.Helper()
	user, ok := item["user"].(map[string]any)
	if !ok {
		t.Fatalf("item has no user: %v", item)
	}
	id, _ := user["id"].(string)
	return id
}

// ─── Follow ──────────────────────────────────────────────────────────────────

func TestFollow_IsIdempotentAndEmitsOneOutboxEvent(t *testing.T) {
	h := newHarness(t)
	aliceToken, aliceID := h.register("alice")
	_, bobID := h.register("bob")

	for i := 0; i < 2; i++ {
		status, body := h.request(http.MethodPost, "/users/"+bobID+"/follow", "", aliceToken)
		if status != http.StatusOK {
			t.Fatalf("follow %d = %d: %s", i, status, body)
		}
		if following, _ := decodeData(t, body)["following"].(bool); !following {
			t.Errorf("follow %d = %s, want following true", i, body)
		}
	}

	if count := h.followRowCount(aliceID, bobID); count != 1 {
		t.Errorf("follow rows = %d, want 1", count)
	}
	if count := h.outboxCount("user.followed"); count != 1 {
		t.Errorf("user.followed outbox events = %d, want 1", count)
	}
}

func TestFollow_Self(t *testing.T) {
	h := newHarness(t)
	token, id := h.register("self")

	status, body := h.request(http.MethodPost, "/users/"+id+"/follow", "", token)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	code, message := errorCodeAndMessage(t, body)
	if code != "VALIDATION_ERROR" || message != "You cannot follow yourself." {
		t.Errorf("error = %s/%s", code, message)
	}
}

func TestFollow_TargetNotFound(t *testing.T) {
	h := newHarness(t)
	token, _ := h.register("follower")

	status, body := h.request(http.MethodPost, "/users/"+uuid.NewString()+"/follow", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if _, message := errorCodeAndMessage(t, body); message != "User not found." {
		t.Errorf("message = %q", message)
	}
}

func TestFollow_InactiveTargetIsNotFound(t *testing.T) {
	h := newHarness(t)
	token, _ := h.register("follower")
	_, targetID := h.register("inactive")

	if _, err := h.db.Pool.Exec(context.Background(),
		`UPDATE "User" SET "isActive" = false WHERE "id" = $1`, targetID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	status, body := h.request(http.MethodPost, "/users/"+targetID+"/follow", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if _, message := errorCodeAndMessage(t, body); message != "User not found." {
		t.Errorf("message = %q", message)
	}
}

func TestFollow_BlockedEitherWayIsForbidden(t *testing.T) {
	h := newHarness(t)
	aliceToken, aliceID := h.register("alice")
	bobToken, bobID := h.register("bob")

	status, body := h.request(http.MethodPost, "/users/"+aliceID+"/block", "", bobToken)
	if status != http.StatusOK {
		t.Fatalf("block = %d: %s", status, body)
	}

	status, body = h.request(http.MethodPost, "/users/"+bobID+"/follow", "", aliceToken)
	if status != http.StatusForbidden {
		t.Fatalf("blocked follow = %d, want 403: %s", status, body)
	}
	code, message := errorCodeAndMessage(t, body)
	if code != "FORBIDDEN" || message != "You cannot follow this user." {
		t.Errorf("error = %s/%s", code, message)
	}

	// Blocking is directional for the constraint but enforced both ways.
	status, body = h.request(http.MethodPost, "/users/"+aliceID+"/follow", "", bobToken)
	if status != http.StatusForbidden {
		t.Fatalf("reverse follow = %d, want 403: %s", status, body)
	}
}

func TestFollow_InvalidUUIDPath(t *testing.T) {
	h := newHarness(t)
	token, _ := h.register("follower")

	status, body := h.request(http.MethodPost, "/users/not-a-uuid/follow", "", token)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	code, message := errorCodeAndMessage(t, body)
	if code != "VALIDATION_ERROR" || message != "Validation failed (uuid is expected)" {
		t.Errorf("error = %s/%s", code, message)
	}
	if strings.Contains(body, `"details"`) {
		t.Errorf("ParseUUIDPipe errors carry no details, got %s", body)
	}
}

func TestFollow_RequiresAuthentication(t *testing.T) {
	h := newHarness(t)
	_, targetID := h.register("target")

	status, body := h.request(http.MethodPost, "/users/"+targetID+"/follow", "", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", status, body)
	}
}

func TestUnfollow_IsIdempotent(t *testing.T) {
	h := newHarness(t)
	aliceToken, aliceID := h.register("alice")
	_, bobID := h.register("bob")

	if status, body := h.request(http.MethodPost, "/users/"+bobID+"/follow", "", aliceToken); status != http.StatusOK {
		t.Fatalf("follow = %d: %s", status, body)
	}
	for i := 0; i < 2; i++ {
		status, body := h.request(http.MethodDelete, "/users/"+bobID+"/follow", "", aliceToken)
		if status != http.StatusOK {
			t.Fatalf("unfollow %d = %d: %s", i, status, body)
		}
		if following, _ := decodeData(t, body)["following"].(bool); following {
			t.Errorf("unfollow %d = %s, want following false", i, body)
		}
	}
	if count := h.followRowCount(aliceID, bobID); count != 0 {
		t.Errorf("follow rows = %d, want 0", count)
	}
}

// ─── Follower / following lists ──────────────────────────────────────────────

func TestFollowers_PagingAndOrdering(t *testing.T) {
	h := newHarness(t)
	_, targetID := h.register("target")
	aliceToken, aliceID := h.register("alice")
	carolToken, carolID := h.register("carol")
	daveToken, daveID := h.register("dave")
	// A fourth viewer is needed only to read the list.
	viewerToken, _ := h.register("viewer")

	for _, token := range []string{aliceToken, carolToken, daveToken} {
		if status, body := h.request(http.MethodPost, "/users/"+targetID+"/follow", "", token); status != http.StatusOK {
			t.Fatalf("follow = %d: %s", status, body)
		}
	}
	base := time.Now().UTC().Add(-time.Hour)
	h.setFollowCreatedAt(aliceID, targetID, base)
	h.setFollowCreatedAt(carolID, targetID, base.Add(time.Minute))
	h.setFollowCreatedAt(daveID, targetID, base.Add(2*time.Minute))

	status, body := h.request(http.MethodGet, "/users/"+targetID+"/followers?limit=2", "", viewerToken)
	if status != http.StatusOK {
		t.Fatalf("followers = %d: %s", status, body)
	}
	first := decodePage(t, body)
	if len(first.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(first.Items))
	}
	if got := itemUserID(t, first.Items[0]); got != daveID {
		t.Errorf("first item = %s, want newest (dave)", got)
	}
	if got := itemUserID(t, first.Items[1]); got != carolID {
		t.Errorf("second item = %s, want carol", got)
	}
	if first.NextCursor == nil {
		t.Fatal("nextCursor = nil, want cursor")
	}

	status, body = h.request(http.MethodGet,
		"/users/"+targetID+"/followers?limit=2&cursor="+*first.NextCursor, "", viewerToken)
	if status != http.StatusOK {
		t.Fatalf("followers page 2 = %d: %s", status, body)
	}
	second := decodePage(t, body)
	if len(second.Items) != 1 {
		t.Fatalf("page 2 items = %d, want 1", len(second.Items))
	}
	if got := itemUserID(t, second.Items[0]); got != aliceID {
		t.Errorf("page 2 item = %s, want alice", got)
	}
	if second.NextCursor != nil {
		t.Errorf("page 2 nextCursor = %q, want nil", *second.NextCursor)
	}

	// A page that exactly fills its limit reports the end of the list.
	status, body = h.request(http.MethodGet, "/users/"+targetID+"/followers?limit=3", "", viewerToken)
	if status != http.StatusOK {
		t.Fatalf("followers exact = %d: %s", status, body)
	}
	if exact := decodePage(t, body); exact.NextCursor != nil {
		t.Errorf("exactly-full page nextCursor = %q, want nil", *exact.NextCursor)
	}
}

func TestFollowers_ExcludeBlockedUsers(t *testing.T) {
	h := newHarness(t)
	targetToken, targetID := h.register("target")
	aliceToken, _ := h.register("alice")
	carolToken, carolID := h.register("carol")
	malloryToken, malloryID := h.register("mallory")

	for _, token := range []string{aliceToken, carolToken, malloryToken} {
		if status, body := h.request(http.MethodPost, "/users/"+targetID+"/follow", "", token); status != http.StatusOK {
			t.Fatalf("follow = %d: %s", status, body)
		}
	}
	if status, body := h.request(http.MethodPost, "/users/"+malloryID+"/block", "", aliceToken); status != http.StatusOK {
		t.Fatalf("block = %d: %s", status, body)
	}

	status, body := h.request(http.MethodGet, "/users/"+targetID+"/followers", "", aliceToken)
	if status != http.StatusOK {
		t.Fatalf("followers = %d: %s", status, body)
	}
	seen := map[string]bool{}
	for _, item := range decodePage(t, body).Items {
		seen[itemUserID(t, item)] = true
	}
	if seen[malloryID] {
		t.Error("blocked user appeared in a third party's follower list")
	}
	if !seen[carolID] {
		t.Error("unblocked follower missing from list")
	}

	// The target viewing its own list is unfiltered.
	status, body = h.request(http.MethodGet, "/users/"+targetID+"/followers", "", targetToken)
	if status != http.StatusOK {
		t.Fatalf("self followers = %d: %s", status, body)
	}
	found := false
	for _, item := range decodePage(t, body).Items {
		if itemUserID(t, item) == malloryID {
			found = true
		}
	}
	if !found {
		t.Error("self follower list should not filter blocked users")
	}
}

func TestFollowing_List(t *testing.T) {
	h := newHarness(t)
	_, aliceID := h.register("alice")
	_, carolID := h.register("carol")
	bobToken, bobID := h.register("bob")

	for _, target := range []string{aliceID, carolID} {
		if status, body := h.request(http.MethodPost, "/users/"+target+"/follow", "", bobToken); status != http.StatusOK {
			t.Fatalf("follow = %d: %s", status, body)
		}
	}

	status, body := h.request(http.MethodGet, "/users/"+bobID+"/following", "", bobToken)
	if status != http.StatusOK {
		t.Fatalf("following = %d: %s", status, body)
	}
	if items := decodePage(t, body).Items; len(items) != 2 {
		t.Errorf("items = %d, want 2", len(items))
	}
}

func TestFollowers_TargetNotFound(t *testing.T) {
	h := newHarness(t)
	token, _ := h.register("viewer")

	status, body := h.request(http.MethodGet, "/users/"+uuid.NewString()+"/followers", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if _, message := errorCodeAndMessage(t, body); message != "User not found." {
		t.Errorf("message = %q", message)
	}
}

// ─── Block ───────────────────────────────────────────────────────────────────

func TestBlock_SeversFollowsBothWaysAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	aliceToken, aliceID := h.register("alice")
	bobToken, bobID := h.register("bob")

	if status, body := h.request(http.MethodPost, "/users/"+bobID+"/follow", "", aliceToken); status != http.StatusOK {
		t.Fatalf("alice follows bob = %d: %s", status, body)
	}
	if status, body := h.request(http.MethodPost, "/users/"+aliceID+"/follow", "", bobToken); status != http.StatusOK {
		t.Fatalf("bob follows alice = %d: %s", status, body)
	}

	for i := 0; i < 2; i++ {
		status, body := h.request(http.MethodPost, "/users/"+bobID+"/block", "", aliceToken)
		if status != http.StatusOK {
			t.Fatalf("block %d = %d: %s", i, status, body)
		}
		if blocked, _ := decodeData(t, body)["blocked"].(bool); !blocked {
			t.Errorf("block %d = %s, want blocked true", i, body)
		}
	}
	if count := h.followRowCount(aliceID, bobID); count != 0 {
		t.Errorf("alice->bob follow rows = %d, want 0", count)
	}
	if count := h.followRowCount(bobID, aliceID); count != 0 {
		t.Errorf("bob->alice follow rows = %d, want 0", count)
	}

	for i := 0; i < 2; i++ {
		status, body := h.request(http.MethodDelete, "/users/"+bobID+"/block", "", aliceToken)
		if status != http.StatusOK {
			t.Fatalf("unblock %d = %d: %s", i, status, body)
		}
		if blocked, _ := decodeData(t, body)["blocked"].(bool); blocked {
			t.Errorf("unblock %d = %s, want blocked false", i, body)
		}
	}
}

func TestBlock_Self(t *testing.T) {
	h := newHarness(t)
	token, id := h.register("self")

	status, body := h.request(http.MethodPost, "/users/"+id+"/block", "", token)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	code, message := errorCodeAndMessage(t, body)
	if code != "VALIDATION_ERROR" || message != "You cannot block yourself." {
		t.Errorf("error = %s/%s", code, message)
	}
}

func TestBlock_TargetNotFound(t *testing.T) {
	h := newHarness(t)
	token, _ := h.register("blocker")

	status, body := h.request(http.MethodPost, "/users/"+uuid.NewString()+"/block", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if _, message := errorCodeAndMessage(t, body); message != "User not found." {
		t.Errorf("message = %q", message)
	}
}

func TestUnblock_TargetNotFoundIsIdempotent(t *testing.T) {
	h := newHarness(t)
	token, _ := h.register("blocker")

	// deleteMany in the reference is a no-op for an unknown target.
	status, body := h.request(http.MethodDelete, "/users/"+uuid.NewString()+"/block", "", token)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	if blocked, _ := decodeData(t, body)["blocked"].(bool); blocked {
		t.Errorf("body = %s, want blocked false", body)
	}
}
