package messaging

import "time"

// ConversationPeerView is the safe user subset embedded in ConversationView.
type ConversationPeerView struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// LastMessageView is the last-message summary embedded in ConversationView.
type LastMessageView struct {
	ID        string     `json:"id"`
	SenderID  string     `json:"senderId"`
	Body      *string    `json:"body"`
	CreatedAt time.Time  `json:"createdAt"`
	DeletedAt *time.Time `json:"deletedAt"`
	IsDeleted bool       `json:"isDeleted"`
}

// ConversationView is the public projection of a Conversation row.
type ConversationView struct {
	ID            string               `json:"id"`
	Type          string               `json:"type"`
	Participant   ConversationPeerView `json:"participant"`
	LastMessage   *LastMessageView     `json:"lastMessage"`
	LastMessageAt *time.Time           `json:"lastMessageAt"`
	UnreadCount   int                  `json:"unreadCount"`
	CreatedAt     time.Time            `json:"createdAt"`
}

// MessageView is the public projection of a Message row.
type MessageView struct {
	ID              string     `json:"id"`
	ConversationID  string     `json:"conversationId"`
	SenderID        string     `json:"senderId"`
	ClientMessageID string     `json:"clientMessageId"`
	Body            *string    `json:"body"`
	DeletedAt       *time.Time `json:"deletedAt"`
	IsDeleted       bool       `json:"isDeleted"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// CursorKey implements httpx.CursorItem for message pagination.
func (m MessageView) CursorKey() (time.Time, string) {
	return m.CreatedAt, m.ID
}

// messageRow is the internal row representation for Message.
type messageRow struct {
	ID              string
	ConversationID  string
	SenderID        string
	ClientMessageID string
	Body            string
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func toMessageView(m messageRow) MessageView {
	deleted := m.DeletedAt != nil
	var body *string
	if !deleted {
		b := m.Body
		body = &b
	}
	return MessageView{
		ID:              m.ID,
		ConversationID:  m.ConversationID,
		SenderID:        m.SenderID,
		ClientMessageID: m.ClientMessageID,
		Body:            body,
		DeletedAt:       m.DeletedAt,
		IsDeleted:       deleted,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func toLastMessageView(m messageRow) LastMessageView {
	deleted := m.DeletedAt != nil
	var body *string
	if !deleted {
		b := m.Body
		body = &b
	}
	return LastMessageView{
		ID:        m.ID,
		SenderID:  m.SenderID,
		Body:      body,
		CreatedAt: m.CreatedAt,
		DeletedAt: m.DeletedAt,
		IsDeleted: deleted,
	}
}

// directKey returns the stable deterministic key for a DM between two users.
// It sorts the two IDs lexicographically and joins them with ":".
func directKey(a, b string) string {
	if a < b {
		return a + ":" + b
	}
	return b + ":" + a
}
