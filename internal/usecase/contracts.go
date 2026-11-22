// Package usecase declares the use cases the HTTP handlers depend on.
package usecase

import (
	"context"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// Notes manages the notes of the calling user.
type Notes interface {
	// List returns a page after cursor ("" for the first page) of at most limit notes.
	List(ctx context.Context, owner uuid.UUID, cursor string, limit int) (pagination.CursorPage[entity.Note], error)
	Create(ctx context.Context, owner uuid.UUID, title, content string) (*entity.Note, error)
	Get(ctx context.Context, owner, id uuid.UUID) (*entity.Note, error)
	Update(ctx context.Context, owner, id uuid.UUID, c *entity.NoteChanges) (*entity.Note, error)
	Delete(ctx context.Context, owner, id uuid.UUID) error
}

// Client is the device a request comes from.
type Client struct {
	IP        string
	UserAgent string
	Locale    string
}

// Caller is the signed-in user of a request.
type Caller struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	AuthTime  time.Time
}

// LoginResult is the login response of platform conventions §5.3.
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64
	User         *entity.User
}

// Registration is a sign-up with email and password.
type Registration struct {
	Email       string
	Password    string
	DisplayName string
}

// Auth signs users in and manages their credentials.
type Auth interface {
	Register(ctx context.Context, c *Client, r *Registration) (*LoginResult, error)
	Login(ctx context.Context, c *Client, email, password string) (*LoginResult, error)
	VerifyEmail(ctx context.Context, token string) error
	ResendVerification(ctx context.Context, caller *Caller, locale string) error
	ForgotPassword(ctx context.Context, c *Client, email string) error
	// ResetPassword returns the user whose password it set.
	ResetPassword(ctx context.Context, token, password string) (uuid.UUID, error)
	Refresh(ctx context.Context, c *Client, refreshToken string) (*LoginResult, error)
	Logout(ctx context.Context, caller *Caller) error
}

// SessionView is a session of the caller, flagged when it is the caller's own.
type SessionView struct {
	Session entity.Session
	Current bool
}

// Sessions manages the sessions of the calling user.
type Sessions interface {
	List(ctx context.Context, caller *Caller) ([]SessionView, error)
	Revoke(ctx context.Context, caller *Caller, id uuid.UUID) error
	RevokeOthers(ctx context.Context, caller *Caller) error
}

// PasswordChange is a changePassword request; Current is nil when it was omitted.
type PasswordChange struct {
	Current *string
	New     string
}

// NewAccount is a user created by an administrator.
type NewAccount struct {
	Email       *string
	Phone       *string
	Password    *string
	DisplayName *string
}

// UserQuery selects a page of users.
type UserQuery struct {
	Keyword  *string
	Status   *string
	Role     *string
	Sort     string
	Page     int
	PageSize int
}

// Users serves the current user and the user administration.
type Users interface {
	Me(ctx context.Context, caller *Caller) (*entity.User, error)
	UpdateMe(ctx context.Context, caller *Caller, displayName *string, avatarFileID *uuid.UUID) (*entity.User, error)
	ChangePassword(ctx context.Context, caller *Caller, c *PasswordChange) error
	List(ctx context.Context, q *UserQuery) (pagination.Page[entity.User], error)
	Create(ctx context.Context, a *NewAccount) (*entity.User, error)
	Get(ctx context.Context, id uuid.UUID) (*entity.User, error)
	Update(ctx context.Context, id uuid.UUID, displayName, status *string) (*entity.User, error)
	SetRoles(ctx context.Context, id uuid.UUID, roles []string) (*entity.User, error)
}
