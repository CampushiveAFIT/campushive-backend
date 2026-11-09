package users

import "time"

type Role string

const (
	RoleStudent         Role = "STUDENT"
	RoleStaff           Role = "STAFF"
	RoleModerator       Role = "MODERATOR"
	RoleUniversityAdmin Role = "UNIVERSITY_ADMIN"
)

type VerificationStatus string

const (
	VerificationUnverified VerificationStatus = "UNVERIFIED"
	VerificationPending    VerificationStatus = "PENDING"
	VerificationVerified   VerificationStatus = "VERIFIED"
	VerificationRejected   VerificationStatus = "REJECTED"
)

// User is a row of the "User" table.
type User struct {
	ID                 string
	Email              string
	PasswordHash       string
	DisplayName        *string
	AvatarURL          *string
	Bio                *string
	Role               Role
	VerificationStatus VerificationStatus
	IsActive           bool
	EmailVerifiedAt    *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// SafeUser is the public projection. passwordHash and isActive never leave the
// service layer, matching the NestJS SafeUser shape and field order.
type SafeUser struct {
	ID                 string             `json:"id"`
	Email              string             `json:"email"`
	DisplayName        *string            `json:"displayName"`
	AvatarURL          *string            `json:"avatarUrl"`
	Bio                *string            `json:"bio"`
	Role               Role               `json:"role"`
	VerificationStatus VerificationStatus `json:"verificationStatus"`
	EmailVerifiedAt    *time.Time         `json:"emailVerifiedAt"`
	CreatedAt          time.Time          `json:"createdAt"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

func ToSafeUser(user User) SafeUser {
	return SafeUser{
		ID:                 user.ID,
		Email:              user.Email,
		DisplayName:        user.DisplayName,
		AvatarURL:          user.AvatarURL,
		Bio:                user.Bio,
		Role:               user.Role,
		VerificationStatus: user.VerificationStatus,
		EmailVerifiedAt:    user.EmailVerifiedAt,
		CreatedAt:          user.CreatedAt,
		UpdatedAt:          user.UpdatedAt,
	}
}

const UserColumns = `"id", "email", "passwordHash", "displayName", "avatarUrl", "bio", "role", "verificationStatus", "isActive", "emailVerifiedAt", "createdAt", "updatedAt"`

func ScanUser(row interface{ Scan(...any) error }) (User, error) {
	var user User
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.DisplayName,
		&user.AvatarURL,
		&user.Bio,
		&user.Role,
		&user.VerificationStatus,
		&user.IsActive,
		&user.EmailVerifiedAt,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	return user, err
}
