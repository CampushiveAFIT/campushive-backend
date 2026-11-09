package httpx

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPageLimit = 20
	MaxPageLimit     = 100
)

type Cursor struct {
	CreatedAt time.Time
	ID        string
}

type CursorItem interface {
	CursorKey() (time.Time, string)
}

type Page struct {
	Limit  int
	Cursor *Cursor
}

type PageResult[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

func EncodeCursor(createdAt time.Time, id string) string {
	payload, err := json.Marshal(map[string]string{
		"createdAt": createdAt.UTC().Format(time.RFC3339Nano),
		"id":        id,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeCursor(raw string) (Cursor, error) {
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return Cursor{}, ValidationMessage("Invalid pagination cursor.")
	}
	var parsed struct {
		CreatedAt *string `json:"createdAt"`
		ID        *string `json:"id"`
	}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return Cursor{}, ValidationMessage("Invalid pagination cursor.")
	}
	if parsed.ID == nil || parsed.CreatedAt == nil {
		return Cursor{}, ValidationMessage("Invalid pagination cursor.")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, *parsed.CreatedAt)
	if err != nil {
		return Cursor{}, ValidationMessage("Invalid pagination cursor.")
	}
	return Cursor{CreatedAt: createdAt, ID: *parsed.ID}, nil
}

func ParsePage(r *http.Request) (Page, error) {
	return ParsePageWith(r, DefaultPageLimit, MaxPageLimit)
}

func ParsePageWith(r *http.Request, defaultLimit, maxLimit int) (Page, error) {
	page := Page{Limit: defaultLimit}
	if raw, ok := rawQueryValue(r.URL.RawQuery, "limit"); ok {
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || n < 1 || n > maxLimit {
			return Page{}, ValidationMessage(fmt.Sprintf("limit must be an integer between 1 and %d.", maxLimit))
		}
		page.Limit = n
	}
	if raw, ok := rawQueryValue(r.URL.RawQuery, "cursor"); ok && strings.TrimSpace(raw) != "" {
		cursor, err := DecodeCursor(raw)
		if err != nil {
			return Page{}, err
		}
		page.Cursor = &cursor
	}
	return page, nil
}

func rawQueryValue(rawQuery string, key string) (string, bool) {
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		decodedKey, err := url.QueryUnescape(rawKey)
		if err != nil || decodedKey != key {
			continue
		}
		decodedValue, err := url.QueryUnescape(rawValue)
		if err != nil {
			return rawValue, true
		}
		return decodedValue, true
	}
	return "", false
}

// ToPage slices limit+1 fetched rows (ordered newest-first) into a page. A
// cursor is returned only when more rows exist, so a page that exactly fills
// its limit reports the end of the list — the same rule the legacy toPage
// helper applies.
func ToPage[T CursorItem](rows []T, limit int) PageResult[T] {
	more := len(rows) > limit
	items := make([]T, 0, limit)
	if more {
		items = rows[:limit]
	} else {
		items = rows
	}
	result := PageResult[T]{Items: items}
	if more && len(items) > 0 {
		createdAt, id := items[len(items)-1].CursorKey()
		cursor := EncodeCursor(createdAt, id)
		result.NextCursor = &cursor
	}
	return result
}

// BuildPage is ToPage for row types that do not implement CursorItem. key
// extracts the (createdAt, id) pair used to build the next cursor.
func BuildPage[T any](rows []T, limit int, key func(T) (time.Time, string)) PageResult[T] {
	more := len(rows) > limit
	items := make([]T, 0, limit)
	if more {
		items = rows[:limit]
	} else if rows != nil {
		items = rows
	}
	result := PageResult[T]{Items: items}
	if more && len(items) > 0 {
		createdAt, id := key(items[len(items)-1])
		cursor := EncodeCursor(createdAt, id)
		result.NextCursor = &cursor
	}
	return result
}

func sortStrings(values []string) {
	sort.Strings(values)
}

func OlderThanSQL(c Cursor) string {
	return "(createdAt < $1 OR (createdAt = $1 AND id < $2))"
}

func NewerThanSQL(c Cursor) string {
	return "(createdAt > $1 OR (createdAt = $1 AND id > $2))"
}
