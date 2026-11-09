package communities

import "time"

// CommunityView is the public projection of a Community row.
type CommunityView struct {
	ID           string    `json:"id"`
	UniversityID string    `json:"universityId"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Description  *string   `json:"description"`
	Type         string    `json:"type"`
	Visibility   string    `json:"visibility"`
	OwnerID      *string   `json:"ownerId"`
	IsActive     bool      `json:"isActive"`
	MemberCount  int       `json:"memberCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// CommunityDetail is returned by GET /communities/:id.
type CommunityDetail struct {
	Community    CommunityView    `json:"community"`
	MyMembership *MembershipShort `json:"myMembership"`
}

// MembershipShort is the minimal membership view embedded in detail/join/leave responses.
type MembershipShort struct {
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

// MemberListItem is returned in the members list.
type MemberListItem struct {
	User      MemberUser `json:"user"`
	Role      string     `json:"role"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"createdAt"`
}

// MemberUser is the safe user subset visible in member lists (no email).
type MemberUser struct {
	ID          string  `json:"id"`
	DisplayName *string `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
}

// communityRow is the internal DB row.
type communityRow struct {
	ID           string
	UniversityID string
	Name         string
	Slug         string
	Description  *string
	Type         string
	Visibility   string
	OwnerID      *string
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

const communityColumns = `"id","universityId","name","slug","description","type","visibility","ownerId","isActive","createdAt","updatedAt"`

func scanCommunity(row interface{ Scan(...any) error }) (communityRow, error) {
	var c communityRow
	err := row.Scan(&c.ID, &c.UniversityID, &c.Name, &c.Slug, &c.Description,
		&c.Type, &c.Visibility, &c.OwnerID, &c.IsActive, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}
