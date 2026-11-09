package notifications

import (
	"encoding/json"
	"time"
)

// NotificationActor is the safe user subset embedded in NotificationView.
type NotificationActor struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// NotificationView is the public projection of a Notification row.
type NotificationView struct {
	ID        string             `json:"id"`
	Type      string             `json:"type"`
	Title     string             `json:"title"`
	Body      *string            `json:"body"`
	Data      json.RawMessage    `json:"data"`
	Actor     *NotificationActor `json:"actor"`
	IsRead    bool               `json:"isRead"`
	ReadAt    *time.Time         `json:"readAt"`
	CreatedAt time.Time          `json:"createdAt"`
}

// PreferenceView is the public projection of a NotificationPreference row.
type PreferenceView struct {
	LikesEnabled        bool       `json:"likesEnabled"`
	CommentsEnabled     bool       `json:"commentsEnabled"`
	FollowsEnabled      bool       `json:"followsEnabled"`
	EventUpdatesEnabled bool       `json:"eventUpdatesEnabled"`
	UpdatedAt           *time.Time `json:"updatedAt"`
}

// notificationRow is the internal row representation used when scanning queries.
type notificationRow struct {
	ID          string
	Type        string
	Title       string
	Body        *string
	Data        json.RawMessage
	IsRead      bool
	ReadAt      *time.Time
	CreatedAt   time.Time
	ActorID     *string
	ActorName   *string
	ActorAvatar *string
}

func toNotificationView(n notificationRow) NotificationView {
	v := NotificationView{
		ID:        n.ID,
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Data:      n.Data,
		IsRead:    n.IsRead,
		ReadAt:    n.ReadAt,
		CreatedAt: n.CreatedAt,
	}
	if n.ActorID != nil {
		v.Actor = &NotificationActor{
			ID:          *n.ActorID,
			DisplayName: n.ActorName,
			AvatarURL:   n.ActorAvatar,
		}
	}
	return v
}
