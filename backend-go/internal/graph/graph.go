package graph

import "time"

// GraphUser is the safe user subset used in follower/following lists.
type GraphUser struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// FollowEntry is one item in a followers/following list.
type FollowEntry struct {
	User      GraphUser `json:"user"`
	CreatedAt time.Time `json:"createdAt"`
}
