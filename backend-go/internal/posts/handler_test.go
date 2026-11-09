package posts_test

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

// accessToken registers and logs in a fresh user, returning its access token.
func (h *harness) accessToken(email string) string {
	h.t.Helper()
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
	tokenEmails.Store(payload.Data.AccessToken, email)
	return payload.Data.AccessToken
}

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

func (h *harness) seedEvent(organizerID, universityID string, communityID *string, status, visibility string) string {
	h.t.Helper()
	id := uuid.NewString()
	now := time.Now()
	if _, err := h.db.Pool.Exec(context.Background(), `
		INSERT INTO "Event" ("id","organizerId","universityId","communityId","title","startsAt","capacity","visibility","status","updatedAt")
		VALUES ($1,$2,$3,$4,$5,$6,10,$7,$8,$6)`,
		id, organizerID, universityID, communityID, "Study Session", now.Add(time.Hour), visibility, status); err != nil {
		h.t.Fatalf("seed event: %v", err)
	}
	return id
}

func (h *harness) joinUniversity(token, universityID string) int {
	h.t.Helper()
	status, body := h.request(http.MethodPost, "/users/me/university",
		fmt.Sprintf(`{"universityId":%q}`, universityID), token)
	if status != http.StatusOK {
		h.t.Logf("joinUniversity = %d %s", status, body)
	}
	return status
}

func (h *harness) getUserID(email string) string {
	h.t.Helper()
	var id string
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "id" FROM "User" WHERE "email" = $1`, email).Scan(&id); err != nil {
		h.t.Fatalf("get user id for %s: %v", email, err)
	}
	return id
}

func (h *harness) createCommunity(token, universityID, name, slug, visibility string) string {
	h.t.Helper()
	status, body := h.request(http.MethodPost, "/communities",
		fmt.Sprintf(`{"universityId":%q,"name":%q,"slug":%q,"visibility":%q}`, universityID, name, slug, visibility), token)
	if status != http.StatusCreated {
		h.t.Fatalf("create community = %d %s", status, body)
	}
	var payload struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		h.t.Fatalf("decode community: %v\n%s", err, body)
	}
	return payload.Data.ID
}

func (h *harness) createPost(token, body string) (int, string) {
	h.t.Helper()
	return h.request(http.MethodPost, "/posts", body, token)
}

// postData decodes the data envelope of a post response.
func (h *harness) postData(t *testing.T, body string) map[string]any {
	t.Helper()
	var payload struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode post: %v\n%s", err, body)
	}
	return payload.Data
}

func (h *harness) postListItems(t *testing.T, body string) []map[string]any {
	t.Helper()
	var payload struct {
		Data struct {
			Items      []map[string]any `json:"items"`
			NextCursor *string          `json:"nextCursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode page: %v\n%s", err, body)
	}
	return payload.Data.Items
}

func validationDetails(t *testing.T, body string) []string {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string   `json:"code"`
			Message string   `json:"message"`
			Details []string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode error: %v\n%s", err, body)
	}
	return payload.Error.Details
}

// setupMember returns a harness with a user joined to a fresh active university.
func setupMember(t *testing.T, prefix string) (h *harness, token, uniID string) {
	t.Helper()
	h = newHarness(t)
	token = h.accessToken(prefix + "-" + uuid.NewString() + "@example.com")
	uniID = h.seedUniversity(prefix+" Uni", prefix+"-uni-"+uuid.NewString(), true)
	if st := h.joinUniversity(token, uniID); st != http.StatusOK {
		t.Fatalf("join university = %d", st)
	}
	return h, token, uniID
}

// ─── Create gates and validation ─────────────────────────────────────────────

func TestCreatePost_RequiresActiveUniversity(t *testing.T) {
	h := newHarness(t)
	token := h.accessToken("nouni-" + uuid.NewString() + "@example.com")
	status, body := h.createPost(token, `{"content":"hello"}`)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", status, body)
	}
	if !strings.Contains(body, "Join an active university before posting.") {
		t.Errorf("body = %s", body)
	}
}

func TestCreatePost_DefaultsVisibilityAndTrimsContent(t *testing.T) {
	h, token, _ := setupMember(t, "defaultvis")
	status, body := h.createPost(token, `{"content":"  hello world  "}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}
	data := h.postData(t, body)
	if data["content"] != "hello world" {
		t.Errorf("content = %v, want trimmed", data["content"])
	}
	if data["visibility"] != "UNIVERSITY" {
		t.Errorf("visibility = %v, want UNIVERSITY", data["visibility"])
	}
	if data["event"] != nil {
		t.Errorf("event = %v, want nil", data["event"])
	}
}

func TestCreatePost_EmptyContent(t *testing.T) {
	h, token, _ := setupMember(t, "emptycontent")
	status, body := h.createPost(token, `{"content":"   "}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	details := validationDetails(t, body)
	if len(details) != 1 || details[0] != "content must not be empty." {
		t.Errorf("details = %v", details)
	}
}

// class-validator applies property decorators bottom-up, so the details run
// MaxLength, MinLength, IsString.
func TestCreatePost_ContentNotString_MessageOrder(t *testing.T) {
	h, token, _ := setupMember(t, "badcontent")
	status, body := h.createPost(token, `{"content":5}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	want := []string{
		"content must be shorter than or equal to 5000 characters",
		"content must not be empty.",
		"content must be a string",
	}
	got := validationDetails(t, body)
	if len(got) != len(want) {
		t.Fatalf("details = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("details[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCreatePost_VisibilityIsCaseSensitive(t *testing.T) {
	h, token, _ := setupMember(t, "visenum")
	status, body := h.createPost(token, `{"content":"hi","visibility":"public"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	want := "visibility must be one of the following values: PUBLIC, UNIVERSITY, COMMUNITY, FOLLOWERS"
	if !strings.Contains(body, want) {
		t.Errorf("body = %s, want %q", body, want)
	}
}

func TestCreatePost_CommunityNotFound(t *testing.T) {
	h, token, _ := setupMember(t, "comnotfound")
	status, body := h.createPost(token,
		fmt.Sprintf(`{"content":"hi","communityId":%q}`, uuid.NewString()))
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if !strings.Contains(body, "Community not found.") {
		t.Errorf("body = %s", body)
	}
}

func TestCreatePost_CommunityRequiresMembership(t *testing.T) {
	h, ownerToken, uniID := setupMember(t, "comowner")
	memberToken := h.accessToken("commember-" + uuid.NewString() + "@example.com")
	h.joinUniversity(memberToken, uniID)

	comID := h.createCommunity(ownerToken, uniID, "Private Club", "private-club-"+uuid.NewString(), "PUBLIC")

	// Non-member cannot post into it.
	status, body := h.createPost(memberToken,
		fmt.Sprintf(`{"content":"hi","communityId":%q}`, comID))
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", status, body)
	}
	if !strings.Contains(body, "Join the community before posting into it.") {
		t.Errorf("body = %s", body)
	}

	// Owner can.
	status, body = h.createPost(ownerToken,
		fmt.Sprintf(`{"content":"hi","communityId":%q}`, comID))
	if status != http.StatusCreated {
		t.Fatalf("owner post = %d: %s", status, body)
	}
}

func TestCreatePost_CommunityVisibilityNeedsCommunity(t *testing.T) {
	h, token, _ := setupMember(t, "comvis")
	status, body := h.createPost(token, `{"content":"hi","visibility":"COMMUNITY"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	if !strings.Contains(body, "Community posts require a community.") {
		t.Errorf("body = %s", body)
	}
}

// ─── Event references ────────────────────────────────────────────────────────

func TestCreatePost_EventNotFound(t *testing.T) {
	h, token, _ := setupMember(t, "evnotfound")
	status, body := h.createPost(token,
		fmt.Sprintf(`{"content":"hi","eventId":%q}`, uuid.NewString()))
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if !strings.Contains(body, "Event not found.") {
		t.Errorf("body = %s", body)
	}
}

func TestCreatePost_EventNotPublished(t *testing.T) {
	h, token, uniID := setupMember(t, "evdraft")
	authorID := h.getUserID(getEmailFromToken(t, h, token))
	eventID := h.seedEvent(authorID, uniID, nil, "DRAFT", "UNIVERSITY")

	status, body := h.createPost(token,
		fmt.Sprintf(`{"content":"hi","eventId":%q}`, eventID))
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	if !strings.Contains(body, "Only published events can be referenced.") {
		t.Errorf("body = %s", body)
	}
	if !strings.Contains(body, "VALIDATION_ERROR") {
		t.Errorf("expected VALIDATION_ERROR: %s", body)
	}
}

func TestCreatePost_EventFromOtherUniversity(t *testing.T) {
	h, token, _ := setupMember(t, "evforeign")
	otherUni := h.seedUniversity("Other Uni", "other-uni-"+uuid.NewString(), true)
	authorID := h.getUserID(getEmailFromToken(t, h, token))
	eventID := h.seedEvent(authorID, otherUni, nil, "PUBLISHED", "UNIVERSITY")

	status, body := h.createPost(token,
		fmt.Sprintf(`{"content":"hi","eventId":%q}`, eventID))
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", status, body)
	}
	if !strings.Contains(body, "Events must belong to your own university.") {
		t.Errorf("body = %s", body)
	}
}

func TestCreatePost_EventCommunityRequiresMembership(t *testing.T) {
	h, ownerToken, uniID := setupMember(t, "evcomowner")
	outsiderToken := h.accessToken("evcomoutsider-" + uuid.NewString() + "@example.com")
	h.joinUniversity(outsiderToken, uniID)

	comID := h.createCommunity(ownerToken, uniID, "Event Club", "event-club-"+uuid.NewString(), "PUBLIC")
	organizerID := h.getUserID(getEmailFromToken(t, h, ownerToken))
	eventID := h.seedEvent(organizerID, uniID, &comID, "PUBLISHED", "UNIVERSITY")

	status, body := h.createPost(outsiderToken,
		fmt.Sprintf(`{"content":"hi","eventId":%q}`, eventID))
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", status, body)
	}
	if !strings.Contains(body, "Community events require community membership.") {
		t.Errorf("body = %s", body)
	}
}

func TestCreatePost_EventBlocked(t *testing.T) {
	h, token, uniID := setupMember(t, "evblocked")
	organizerToken := h.accessToken("evorganizer-" + uuid.NewString() + "@example.com")
	h.joinUniversity(organizerToken, uniID)
	organizerID := h.getUserID(getEmailFromToken(t, h, organizerToken))

	eventID := h.seedEvent(organizerID, uniID, nil, "PUBLISHED", "UNIVERSITY")

	status, _ := h.request(http.MethodPost, "/users/"+organizerID+"/block", "", token)
	if status != http.StatusOK {
		t.Fatalf("block = %d", status)
	}

	status, body := h.createPost(token,
		fmt.Sprintf(`{"content":"hi","eventId":%q}`, eventID))
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", status, body)
	}
	if !strings.Contains(body, "You cannot reference this event.") {
		t.Errorf("body = %s", body)
	}
}

func TestCreatePost_EmbedsEventSummary(t *testing.T) {
	h, token, uniID := setupMember(t, "evsummary")
	authorID := h.getUserID(getEmailFromToken(t, h, token))
	eventID := h.seedEvent(authorID, uniID, nil, "PUBLISHED", "PUBLIC")

	status, body := h.createPost(token,
		fmt.Sprintf(`{"content":"hi","eventId":%q}`, eventID))
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %s", status, body)
	}
	data := h.postData(t, body)
	event, ok := data["event"].(map[string]any)
	if !ok {
		t.Fatalf("event = %v, want object", data["event"])
	}
	if event["id"] != eventID || event["status"] != "PUBLISHED" || event["visibility"] != "PUBLIC" {
		t.Errorf("event = %v", event)
	}
}

// ─── Detail and visibility ───────────────────────────────────────────────────

func TestPostDetail_NotFoundAndInvalidUUID(t *testing.T) {
	h, token, _ := setupMember(t, "detail404")

	status, body := h.request(http.MethodGet, "/posts/"+uuid.NewString(), "", token)
	if status != http.StatusNotFound || !strings.Contains(body, "Post not found.") {
		t.Fatalf("status = %d, body = %s", status, body)
	}

	status, body = h.request(http.MethodGet, "/posts/not-a-uuid", "", token)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", status, body)
	}
	if !strings.Contains(body, "Validation failed (uuid is expected)") {
		t.Errorf("body = %s", body)
	}
	if strings.Contains(body, `"details"`) {
		t.Errorf("ParseUUIDPipe must not emit details: %s", body)
	}
}

// A UNIVERSITY event stays hidden from a viewer outside that university.
func TestPostDetail_EventSummaryHiddenWhenNotVisible(t *testing.T) {
	h, authorToken, uniA := setupMember(t, "evhidea")
	otherToken := h.accessToken("evhideb-" + uuid.NewString() + "@example.com")
	uniB := h.seedUniversity("Hide B", "hide-b-"+uuid.NewString(), true)
	h.joinUniversity(otherToken, uniB)

	authorID := h.getUserID(getEmailFromToken(t, h, authorToken))
	eventID := h.seedEvent(authorID, uniA, nil, "PUBLISHED", "UNIVERSITY")

	// Public post so the other user can read it, but the event is university-only.
	status, body := h.createPost(authorToken,
		fmt.Sprintf(`{"content":"hi","eventId":%q,"visibility":"PUBLIC"}`, eventID))
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	postID := h.postData(t, body)["id"].(string)

	// Author sees the summary.
	status, body = h.request(http.MethodGet, "/posts/"+postID, "", authorToken)
	if status != http.StatusOK {
		t.Fatalf("author detail = %d: %s", status, body)
	}
	if h.postData(t, body)["event"] == nil {
		t.Error("author should see the event summary")
	}

	// Other-university viewer sees the post but not the event.
	status, body = h.request(http.MethodGet, "/posts/"+postID, "", otherToken)
	if status != http.StatusOK {
		t.Fatalf("other detail = %d: %s", status, body)
	}
	if h.postData(t, body)["event"] != nil {
		t.Errorf("event summary leaked: %v", h.postData(t, body)["event"])
	}
}

// ─── Likes ───────────────────────────────────────────────────────────────────

func TestLike_IdempotentAndConcurrent(t *testing.T) {
	h, authorToken, _ := setupMember(t, "likeauthor")
	likerToken := h.accessToken("liker-" + uuid.NewString() + "@example.com")

	status, body := h.createPost(authorToken, `{"content":"like me","visibility":"PUBLIC"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	postID := h.postData(t, body)["id"].(string)

	const attempts = 5
	var wg sync.WaitGroup
	type result struct {
		status int
		body   string
	}
	statuses := make(chan result, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, respBody := h.request(http.MethodPost, "/posts/"+postID+"/like", "", likerToken)
			statuses <- result{st, respBody}
		}()
	}
	wg.Wait()
	close(statuses)
	for res := range statuses {
		if res.status != http.StatusOK {
			t.Errorf("like status = %d, want 200: %s", res.status, res.body)
		}
	}

	var likeCount int
	if err := h.db.Pool.QueryRow(context.Background(),
		`SELECT "likeCount" FROM "Post" WHERE "id" = $1`, postID).Scan(&likeCount); err != nil {
		t.Fatalf("read like count: %v", err)
	}
	if likeCount != 1 {
		t.Errorf("likeCount = %d, want 1 after concurrent likes", likeCount)
	}

	// Unlike when not liked is a no-op.
	status, body = h.request(http.MethodDelete, "/posts/"+postID+"/like", "", authorToken)
	if status != http.StatusOK {
		t.Fatalf("unlike-when-not-liked = %d: %s", status, body)
	}
}

// ─── Author lists ────────────────────────────────────────────────────────────

func TestListByAuthor_SelfSeesHiddenPostsAndLikedByMe(t *testing.T) {
	h, token, _ := setupMember(t, "ownlist")
	authorID := h.getUserID(getEmailFromToken(t, h, token))

	status, body := h.createPost(token, `{"content":"followers only","visibility":"FOLLOWERS"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	postID := h.postData(t, body)["id"].(string)

	// Like own post.
	if st, _ := h.request(http.MethodPost, "/posts/"+postID+"/like", "", token); st != http.StatusOK {
		t.Fatalf("like = %d", st)
	}

	status, body = h.request(http.MethodGet, "/users/"+authorID+"/posts", "", token)
	if status != http.StatusOK {
		t.Fatalf("list = %d: %s", status, body)
	}
	items := h.postListItems(t, body)
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0]["likedByMe"] != true {
		t.Errorf("likedByMe = %v, want true on own list", items[0]["likedByMe"])
	}
}

func TestListByAuthor_BlockedReturnsEmpty(t *testing.T) {
	h, authorToken, _ := setupMember(t, "blockauthor")
	viewerToken := h.accessToken("blockviewer-" + uuid.NewString() + "@example.com")
	authorID := h.getUserID(getEmailFromToken(t, h, authorToken))

	if st, _ := h.createPost(authorToken, `{"content":"hi","visibility":"PUBLIC"}`); st != http.StatusCreated {
		t.Fatalf("create = %d", st)
	}
	if st, _ := h.request(http.MethodPost, "/users/"+authorID+"/block", "", viewerToken); st != http.StatusOK {
		t.Fatalf("block = %d", st)
	}

	status, body := h.request(http.MethodGet, "/users/"+authorID+"/posts", "", viewerToken)
	if status != http.StatusOK {
		t.Fatalf("list = %d: %s", status, body)
	}
	if items := h.postListItems(t, body); len(items) != 0 {
		t.Errorf("items = %d, want 0 for a blocked author", len(items))
	}
}

func TestListByUniversity_NotFound(t *testing.T) {
	h, token, _ := setupMember(t, "unipost404")
	status, body := h.request(http.MethodGet, "/universities/missing-"+uuid.NewString()+"/posts", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if !strings.Contains(body, "University not found.") {
		t.Errorf("body = %s", body)
	}
}

func TestListByCommunity_NotFound(t *testing.T) {
	h, token, _ := setupMember(t, "compost404")
	status, body := h.request(http.MethodGet, "/communities/"+uuid.NewString()+"/posts", "", token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if !strings.Contains(body, "Community not found.") {
		t.Errorf("body = %s", body)
	}
}

// ─── Update, remove, comments ────────────────────────────────────────────────

func TestUpdatePost_OnlyAuthor(t *testing.T) {
	h, authorToken, _ := setupMember(t, "updauthor")
	otherToken := h.accessToken("updother-" + uuid.NewString() + "@example.com")

	status, body := h.createPost(authorToken, `{"content":"original","visibility":"PUBLIC"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	postID := h.postData(t, body)["id"].(string)

	status, body = h.request(http.MethodPatch, "/posts/"+postID, `{"content":"hijack"}`, otherToken)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", status, body)
	}
	if !strings.Contains(body, "Only the author can update this post.") {
		t.Errorf("body = %s", body)
	}

	status, body = h.request(http.MethodPatch, "/posts/"+postID, `{"content":"  edited  "}`, authorToken)
	if status != http.StatusOK {
		t.Fatalf("author update = %d: %s", status, body)
	}
	if got := h.postData(t, body)["content"]; got != "edited" {
		t.Errorf("content = %v, want trimmed", got)
	}
}

func TestRemovePost_SoftDeletes(t *testing.T) {
	h, token, _ := setupMember(t, "removepost")
	status, body := h.createPost(token, `{"content":"bye","visibility":"PUBLIC"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	postID := h.postData(t, body)["id"].(string)

	status, body = h.request(http.MethodDelete, "/posts/"+postID, "", token)
	if status != http.StatusOK {
		t.Fatalf("delete = %d: %s", status, body)
	}

	status, body = h.request(http.MethodGet, "/posts/"+postID, "", token)
	if status != http.StatusNotFound || !strings.Contains(body, "Post not found.") {
		t.Fatalf("after delete: status = %d, body = %s", status, body)
	}
}

func TestComments_CRUD(t *testing.T) {
	h, authorToken, _ := setupMember(t, "commentauthor")
	commenterToken := h.accessToken("commenter-" + uuid.NewString() + "@example.com")

	status, body := h.createPost(authorToken, `{"content":"discuss","visibility":"PUBLIC"}`)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	postID := h.postData(t, body)["id"].(string)

	status, body = h.request(http.MethodPost, "/posts/"+postID+"/comments", `{"content":"  first  "}`, commenterToken)
	if status != http.StatusCreated {
		t.Fatalf("comment create = %d: %s", status, body)
	}
	var created struct {
		Data struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	if created.Data.Content != "first" {
		t.Errorf("content = %q, want trimmed", created.Data.Content)
	}

	status, body = h.request(http.MethodGet, "/posts/"+postID+"/comments", "", commenterToken)
	if status != http.StatusOK {
		t.Fatalf("comment list = %d: %s", status, body)
	}
	if !strings.Contains(body, "first") {
		t.Errorf("list body = %s", body)
	}

	// A stranger may not delete the comment.
	strangerToken := h.accessToken("stranger-" + uuid.NewString() + "@example.com")
	status, body = h.request(http.MethodDelete, "/comments/"+created.Data.ID, "", strangerToken)
	if status != http.StatusForbidden {
		t.Fatalf("stranger delete = %d, want 403: %s", status, body)
	}

	// The comment author may.
	status, body = h.request(http.MethodDelete, "/comments/"+created.Data.ID, "", commenterToken)
	if status != http.StatusOK {
		t.Fatalf("author delete = %d: %s", status, body)
	}
}

func TestComments_NotFound(t *testing.T) {
	h, token, _ := setupMember(t, "comment404")
	status, body := h.request(http.MethodPatch, "/comments/"+uuid.NewString(), `{"content":"x"}`, token)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", status, body)
	}
	if !strings.Contains(body, "Comment not found.") {
		t.Errorf("body = %s", body)
	}

	status, body = h.request(http.MethodDelete, "/comments/"+uuid.NewString(), "", token)
	if status != http.StatusNotFound || !strings.Contains(body, "Comment not found.") {
		t.Fatalf("status = %d, body = %s", status, body)
	}
}

// helpers that avoid threading an email through extra signatures

var tokenEmails sync.Map

func getEmailFromToken(t *testing.T, h *harness, token string) string {
	t.Helper()
	value, ok := tokenEmails.Load(token)
	if !ok {
		t.Fatal("unknown token; register the user with accessToken first")
	}
	return value.(string)
}
