package feed

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"time"

	"campus-hive/backend-go/internal/events"
	"campus-hive/backend-go/internal/httpx"
	"campus-hive/backend-go/internal/posts"
)

// FeedItemType is either "POST" or "EVENT".
type FeedItemType string

const (
	FeedItemPost  FeedItemType = "POST"
	FeedItemEvent FeedItemType = "EVENT"
)

// FeedItem represents a single post or event item in a feed.
type FeedItem struct {
	Type      FeedItemType      `json:"type"`
	ID        string            `json:"id"`
	CreatedAt time.Time         `json:"createdAt"`
	Score     float64           `json:"score"`
	Post      *posts.PostView   `json:"post,omitempty"`
	Event     *events.EventView `json:"event,omitempty"`
}

// FeedPage represents a paginated feed response.
type FeedPage struct {
	Items      []FeedItem `json:"items"`
	NextCursor *string    `json:"nextCursor"`
}

// FeedCursor holds decoded keyset values for ranking.
type FeedCursor struct {
	Score     float64   `json:"score"`
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

// ScorePost calculates the deterministic ranking score for a post:
// createdAt.UnixMilli()/3600000 + likeCount*2 + commentCount*3.
func ScorePost(createdAt time.Time, likeCount, commentCount int) float64 {
	return float64(createdAt.UnixMilli())/3600000.0 + float64(likeCount*2) + float64(commentCount*3)
}

// ScoreEvent calculates the deterministic ranking score for an event:
// createdAt.UnixMilli()/3600000 + registeredCount*1.
func ScoreEvent(createdAt time.Time, registeredCount int) float64 {
	return float64(createdAt.UnixMilli())/3600000.0 + float64(registeredCount)
}

// EncodeCursor encodes a FeedCursor into base64url JSON.
func EncodeCursor(score float64, createdAt time.Time, id string) string {
	payload, err := json.Marshal(map[string]any{
		"score":     score,
		"createdAt": createdAt.UTC().Format(time.RFC3339Nano),
		"id":        id,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

// DecodeCursor decodes a base64url string into a FeedCursor.
func DecodeCursor(raw string) (*FeedCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, httpx.ValidationMessage("Invalid feed cursor.")
	}

	var parsed struct {
		Score     *float64 `json:"score"`
		CreatedAt *string  `json:"createdAt"`
		ID        *string  `json:"id"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, httpx.ValidationMessage("Invalid feed cursor.")
	}
	if parsed.Score == nil || math.IsNaN(*parsed.Score) || math.IsInf(*parsed.Score, 0) ||
		parsed.ID == nil || *parsed.ID == "" || parsed.CreatedAt == nil {
		return nil, httpx.ValidationMessage("Invalid feed cursor.")
	}

	t, err := time.Parse(time.RFC3339Nano, *parsed.CreatedAt)
	if err != nil {
		t, err = time.Parse(time.RFC3339, *parsed.CreatedAt)
		if err != nil {
			return nil, httpx.ValidationMessage("Invalid feed cursor.")
		}
	}

	return &FeedCursor{
		Score:     *parsed.Score,
		CreatedAt: t.UTC(),
		ID:        *parsed.ID,
	}, nil
}
