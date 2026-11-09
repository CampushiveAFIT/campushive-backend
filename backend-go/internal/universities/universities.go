package universities

import "time"

// UniversityView is the public projection of a University row.
type UniversityView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Slug        string    `json:"slug"`
	Domain      *string   `json:"domain"`
	Description *string   `json:"description"`
	LogoURL     *string   `json:"logoUrl"`
	IsActive    bool      `json:"isActive"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// UniversityDetail is returned by GET /universities/:slug.
type UniversityDetail struct {
	UniversityView
	MemberCount    int `json:"memberCount"`
	CommunityCount int `json:"communityCount"`
}

// MembershipView is returned by campus identity endpoints.
type MembershipView struct {
	ID                 string         `json:"id"`
	Status             string         `json:"status"`
	Role               string         `json:"role"`
	VerificationStatus string         `json:"verificationStatus"`
	University         UniversityView `json:"university"`
	CreatedAt          time.Time      `json:"createdAt"`
	UpdatedAt          time.Time      `json:"updatedAt"`
}

// universityRow is the internal DB row shape.
type universityRow struct {
	ID          string
	Name        string
	Slug        string
	Domain      *string
	Description *string
	LogoURL     *string
	IsActive    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const universityColumns = `"id","name","slug","domain","description","logoUrl","isActive","createdAt","updatedAt"`

func scanUniversity(row interface{ Scan(...any) error }) (universityRow, error) {
	var u universityRow
	err := row.Scan(&u.ID, &u.Name, &u.Slug, &u.Domain, &u.Description, &u.LogoURL, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

func toView(u universityRow) UniversityView {
	return UniversityView{
		ID:          u.ID,
		Name:        u.Name,
		Slug:        u.Slug,
		Domain:      u.Domain,
		Description: u.Description,
		LogoURL:     u.LogoURL,
		IsActive:    u.IsActive,
		CreatedAt:   u.CreatedAt,
		UpdatedAt:   u.UpdatedAt,
	}
}
