package entity

import (
	"strings"
	"time"
	"uuid"
)

// User statuses.
const (
	UserActive   = "active"
	UserDisabled = "disabled"
)

// User is an account of the issuing service.
type User struct {
	ID            uuid.UUID
	Email         *string
	EmailVerified bool
	Phone         *string
	DisplayName   string
	AvatarFileID  *uuid.UUID
	Status        string
	Roles         []string
	HasPassword   bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Active reports whether the user may sign in.
func (u *User) Active() bool {
	return u.Status == UserActive
}

// Session is a signed-in device of a user.
type Session struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	AuthTime     time.Time
	ExpiresAt    time.Time
	UserAgent    string
	IP           string
	CreatedAt    time.Time
	LastActiveAt time.Time
}

// NormalizeEmail trims and lowercases an email address (platform conventions §4).
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
