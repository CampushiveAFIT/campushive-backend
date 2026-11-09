package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type row struct {
	ID        string
	CreatedAt time.Time
}

func (r row) CursorKey() (time.Time, string) {
	return r.CreatedAt, r.ID
}

func TestCursorRoundTrip(t *testing.T) {
	createdAt := time.Date(2026, 9, 25, 10, 30, 0, 123456789, time.UTC)
	encoded := EncodeCursor(createdAt, "row-1")
	if encoded == "" {
		t.Fatal("EncodeCursor returned empty string")
	}
	for _, r := range encoded {
		if r == '+' || r == '/' || r == '=' {
			t.Errorf("cursor is not base64url: %q", encoded)
		}
	}
	cursor, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if cursor.ID != "row-1" {
		t.Errorf("id = %q, want row-1", cursor.ID)
	}
	if !cursor.CreatedAt.Equal(createdAt) {
		t.Errorf("createdAt = %v, want %v", cursor.CreatedAt, createdAt)
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	cases := []string{
		"not-base64!!",
		"eyJmb28iOiJiYXIifQ",
		"eyJpZCI6IjEiLCJjcmVhdGVkQXQiOiJub3QtYS1kYXRlIn0",
		"eyJpZCI6MTIzfQ",
		"eyJjcmVhdGVkQXQiOiIyMDI2LTA5LTI1VDAxMDowMDowMFoifQ",
	}
	for _, raw := range cases {
		if _, err := DecodeCursor(raw); err == nil {
			t.Errorf("DecodeCursor(%q) should fail", raw)
		} else {
			_, code, message, _ := StatusForError(err)
			if code != CodeValidationError || message != "Invalid pagination cursor." {
				t.Errorf("DecodeCursor(%q) error = %s/%s", raw, code, message)
			}
		}
	}
}

func TestParsePage(t *testing.T) {
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cursor := EncodeCursor(createdAt, "abc")

	r := httptest.NewRequest(http.MethodGet, "/api/v1/posts?limit=5&cursor="+cursor, nil)
	page, err := ParsePage(r)
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if page.Limit != 5 {
		t.Errorf("limit = %d, want 5", page.Limit)
	}
	if page.Cursor == nil || page.Cursor.ID != "abc" {
		t.Errorf("cursor = %+v", page.Cursor)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
	page, err = ParsePage(r)
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if page.Limit != DefaultPageLimit {
		t.Errorf("default limit = %d, want %d", page.Limit, DefaultPageLimit)
	}
	if page.Cursor != nil {
		t.Errorf("cursor should be nil, got %+v", page.Cursor)
	}
}

func TestParsePageRejectsInvalidLimits(t *testing.T) {
	for _, raw := range []string{"0", "-1", "101", "abc", "1.5", ""} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/posts?limit="+raw, nil)
		if raw == "" {
			r = httptest.NewRequest(http.MethodGet, "/api/v1/posts?limit=", nil)
		}
		page, err := ParsePage(r)
		if err == nil {
			t.Errorf("limit=%q should be rejected (got %+v)", raw, page)
			continue
		}
		_, code, _, _ := StatusForError(err)
		if code != CodeValidationError {
			t.Errorf("limit=%q code = %s", raw, code)
		}
	}
}

func TestParsePageWithCustomMax(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/feed/home?limit=50", nil)
	page, err := ParsePageWith(r, 20, 50)
	if err != nil {
		t.Fatalf("ParsePageWith: %v", err)
	}
	if page.Limit != 50 {
		t.Errorf("limit = %d, want 50", page.Limit)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/feed/home?limit=51", nil)
	if _, err := ParsePageWith(r, 20, 50); err == nil {
		t.Error("limit above the custom maximum must be rejected")
	}
}

func TestParsePageRejectsInvalidCursor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/posts?cursor=%%%", nil)
	if _, err := ParsePage(r); err == nil {
		t.Fatal("expected cursor rejection")
	}
}

func TestToPage(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	rows := []row{
		{ID: "c", CreatedAt: base},
		{ID: "b", CreatedAt: base.Add(-time.Minute)},
		{ID: "a", CreatedAt: base.Add(-2 * time.Minute)},
	}
	result := ToPage(rows, 2)
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	if result.NextCursor == nil {
		t.Fatal("expected a next cursor when more rows exist")
	}
	cursor, err := DecodeCursor(*result.NextCursor)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if cursor.ID != "b" {
		t.Errorf("next cursor id = %q, want b", cursor.ID)
	}

	result = ToPage(rows, 3)
	if result.NextCursor != nil {
		t.Errorf("next cursor should be null on the last page, got %q", *result.NextCursor)
	}
}

// A page that exactly fills its limit must not advertise a next cursor: only
// the extra row fetched beyond the limit proves there is a next page.
func TestToPageNoCursorWhenPageExactlyFull(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	rows := []row{
		{ID: "b", CreatedAt: base},
		{ID: "a", CreatedAt: base.Add(-time.Minute)},
	}
	result := ToPage(rows, 2)
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	if result.NextCursor != nil {
		t.Errorf("exactly-full page must not emit a cursor, got %q", *result.NextCursor)
	}
}

func TestToPageEmpty(t *testing.T) {
	result := ToPage([]row{}, 5)
	if len(result.Items) != 0 {
		t.Errorf("items = %d, want 0", len(result.Items))
	}
	if result.NextCursor != nil {
		t.Errorf("empty page cursor = %q, want null", *result.NextCursor)
	}
}

func TestBuildPage(t *testing.T) {
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	rows := []row{
		{ID: "c", CreatedAt: base},
		{ID: "b", CreatedAt: base.Add(-time.Minute)},
		{ID: "a", CreatedAt: base.Add(-2 * time.Minute)},
	}
	key := func(r row) (time.Time, string) { return r.CursorKey() }

	result := BuildPage(rows, 2, key)
	if len(result.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(result.Items))
	}
	if result.NextCursor == nil {
		t.Fatal("expected a next cursor when more rows exist")
	}
	cursor, err := DecodeCursor(*result.NextCursor)
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if cursor.ID != "b" {
		t.Errorf("next cursor id = %q, want b", cursor.ID)
	}

	if got := BuildPage(rows, 3, key); got.NextCursor != nil {
		t.Errorf("last page cursor = %q, want null", *got.NextCursor)
	}
	empty := BuildPage(nil, 5, key)
	if empty.Items == nil || len(empty.Items) != 0 {
		t.Errorf("nil rows must yield an empty JSON array, got %#v", empty.Items)
	}
	payload, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"items":[]`) {
		t.Errorf("payload = %s, want an empty items array", payload)
	}
	if !strings.Contains(string(payload), `"nextCursor":null`) {
		t.Errorf("payload = %s, want a null nextCursor", payload)
	}
}

func TestKeysetSQLHelpers(t *testing.T) {
	cursor := Cursor{CreatedAt: time.Now(), ID: "x"}
	if OlderThanSQL(cursor) != "(createdAt < $1 OR (createdAt = $1 AND id < $2))" {
		t.Errorf("OlderThanSQL = %s", OlderThanSQL(cursor))
	}
	if NewerThanSQL(cursor) != "(createdAt > $1 OR (createdAt = $1 AND id > $2))" {
		t.Errorf("NewerThanSQL = %s", NewerThanSQL(cursor))
	}
}
