package entity

import (
	"time"
	"uuid"
)

// Role ids seeded like nestjs RoleEnum.
const (
	RoleAdmin    = 1
	RoleUser     = 2
	RoleEditor   = 3
	RoleReviewer = 4
)

// Status ids seeded like nestjs StatusEnum.
const (
	StatusActive   = 1
	StatusInactive = 2
)

// ProviderEmail is the default user provider.
const ProviderEmail = "email"

// Named is a role or status: an id with a display name.
type Named struct {
	ID   int
	Name string
}

// User is a user with its role, status and photo loaded.
type User struct {
	ID            int
	Email         *string
	Password      *string
	Provider      string
	FirstName     *string
	LastName      *string
	Phone         *string
	Nickname      *string
	Gender        *int
	Birthday      *time.Time
	WechatOpenID  *string
	WechatUnionID *string
	AlipayOpenID  *string
	Photo         *File
	Role          *Named
	Status        *Named
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeletedAt     *time.Time
}

// StatusID is the status id, or 0 without a status.
func (u *User) StatusID() int {
	if u.Status == nil {
		return 0
	}

	return u.Status.ID
}

// CanAuthenticate reports whether the status allows signing in: active or inactive.
func CanAuthenticate(statusID int) bool {
	return statusID == StatusActive || statusID == StatusInactive
}

// File is an uploaded file.
type File struct {
	ID          uuid.UUID
	Path        string
	Visibility  string
	MimeType    *string
	Size        *int
	OwnerUserID *int
}

// File visibilities.
const (
	VisibilityPublic  = "PUBLIC"
	VisibilityPrivate = "PRIVATE"
)

// Session is a signed-in device.
type Session struct {
	ID           int
	Hash         string
	UserID       *int
	UserAgent    *string
	IP           *string
	CreatedAt    time.Time
	LastActiveAt *time.Time
}
