// Package repo declares the storage ports the use cases depend on.
package repo

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

// Transactions registers work that must wait for the request transaction to commit.
type Transactions interface {
	AfterCommit(ctx context.Context, fn func(context.Context))
}

// Cache stores JSON values with a TTL, like nestjs cache-manager.
type Cache interface {
	Get(ctx context.Context, key string, dst any) (bool, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// Pinger checks that a dependency answers.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Locks are expiring mutual-exclusion claims shared by every instance when Redis is enabled.
type Locks interface {
	// TryAcquire claims key for ttl and returns the release token; ok is false when it is taken.
	TryAcquire(ctx context.Context, key string, ttl time.Duration) (token string, ok bool)
	Release(ctx context.Context, key, token string)
}

// Sink receives one uploaded file under a random key.
type Sink interface {
	Key() string
	Write(p []byte) (int, error)
	// Commit stores the received bytes; Discard removes whatever was stored.
	Commit(ctx context.Context) error
	Discard(ctx context.Context)
}

// FileStorage keeps file contents on local disk or in an S3 bucket; path is the file row path.
type FileStorage interface {
	Local() bool
	NewSink(key, contentType string) (Sink, error)
	Delete(ctx context.Context, path string) error
	// LocalFile is the disk path of a local file.
	LocalFile(path string) string
	PresignGet(ctx context.Context, key string) (string, error)
	PresignPut(ctx context.Context, key string, length int64) (string, error)
}

// AuditLog is an audit_log row.
type AuditLog struct {
	ActorSub     *uuid.UUID
	Action       string
	ResourceType string
	ResourceID   string
	RequestID    string
	IP           string
	Data         map[string]any
}

// Audits stores audit logs, in the request transaction when there is one.
type Audits interface {
	Create(ctx context.Context, l *AuditLog) error
}

// ErrIdempotencyClaimLost reports a key whose claim expired or passed to another request.
var ErrIdempotencyClaimLost = errors.New("repo: idempotency claim lost")

// IdempotencyRecord is the stored state of an idempotency key.
type IdempotencyRecord struct {
	Hash string `json:"hash"`
	// Owner identifies the request holding an unfinished claim.
	Owner       string `json:"owner,omitempty"`
	Done        bool   `json:"done"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Body        []byte `json:"body,omitempty"`
}

// IdempotencyStore keeps the idempotency keys.
type IdempotencyStore interface {
	// Begin claims key for owner, a request with body hash; existing is the record stored
	// before, nil when the key was free and is now claimed.
	Begin(ctx context.Context, key, hash, owner string, ttl time.Duration) (existing *IdempotencyRecord, err error)
	// Complete stores the finished response of the key while owner holds its claim, and returns
	// ErrIdempotencyClaimLost otherwise.
	Complete(ctx context.Context, key, owner string, rec *IdempotencyRecord, ttl time.Duration) error
	// Release frees the key while owner holds its claim.
	Release(ctx context.Context, key, owner string) error
}

// NotePage selects the notes of an owner after a position, newest first.
type NotePage struct {
	Owner uuid.UUID
	// AfterCreatedAt and AfterID are the position of the last note of the previous page; nil for
	// the first page.
	AfterCreatedAt *time.Time
	AfterID        *uuid.UUID
	Limit          int
}

// Notes stores notes; reads skip soft-deleted rows and the notes of other owners.
type Notes interface {
	Create(ctx context.Context, n *entity.Note) (*entity.Note, error)
	// Get, Update and Delete report found=false for a note that does not exist or is not owner's.
	Get(ctx context.Context, owner, id uuid.UUID) (n *entity.Note, found bool, err error)
	List(ctx context.Context, p *NotePage) ([]entity.Note, error)
	Update(ctx context.Context, owner, id uuid.UUID, c *entity.NoteChanges) (n *entity.Note, found bool, err error)
	Delete(ctx context.Context, owner, id uuid.UUID) (found bool, err error)
}

// UserRecord is a user with its password hash, nil when no password is set.
type UserRecord struct {
	User         entity.User
	PasswordHash *string
}

// NewUser is a user to create.
type NewUser struct {
	ID            uuid.UUID
	Email         *string
	Phone         *string
	PasswordHash  *string
	DisplayName   string
	EmailVerified bool
	Roles         []string
}

// UserFilter selects a page of users.
type UserFilter struct {
	Keyword *string
	Status  *string
	Role    *string
	// Sort lists at most two keys among createdAt, -createdAt, email and -email; Keyword matches
	// as a plain substring.
	Sort   []string
	Limit  int
	Offset int
}

// UserChanges is a partial update of a user; nil fields keep their value.
type UserChanges struct {
	DisplayName *string
	Status      *string
}

// Users stores the accounts; reads skip soft-deleted users. Create reports a taken email or
// phone as user.emailTaken / user.phoneTaken.
type Users interface {
	Create(ctx context.Context, u *NewUser) (*entity.User, error)
	Get(ctx context.Context, id uuid.UUID) (u *UserRecord, found bool, err error)
	// GetForUpdate locks the user row until the transaction ends.
	GetForUpdate(ctx context.Context, id uuid.UUID) (u *UserRecord, found bool, err error)
	GetByEmail(ctx context.Context, email string) (u *UserRecord, found bool, err error)
	Update(ctx context.Context, id uuid.UUID, c *UserChanges) (u *entity.User, found bool, err error)
	SetPassword(ctx context.Context, id uuid.UUID, hash string) error
	MarkEmailVerified(ctx context.Context, id uuid.UUID) error
	SetRoles(ctx context.Context, id uuid.UUID, roles []string) error
	List(ctx context.Context, f *UserFilter) (users []entity.User, total int, err error)
	// LockAdmins takes the transaction lock every change that may remove an admin holds.
	LockAdmins(ctx context.Context) error
	// CountActiveAdmins counts the active admins other than except; uuid.Nil counts all.
	CountActiveAdmins(ctx context.Context, except uuid.UUID) (int, error)
}

// NewSession is a session to create.
type NewSession struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	RefreshHash []byte
	AuthTime    time.Time
	ExpiresAt   time.Time
	UserAgent   string
	IP          string
}

// SessionRecord is a session with its revocation time.
type SessionRecord struct {
	Session   entity.Session
	RevokedAt *time.Time
}

// RefreshHistory is a rotated refresh token hash.
type RefreshHistory struct {
	SessionID uuid.UUID
	RotatedAt time.Time
}

// Sessions stores the sessions and the refresh token hashes rotated out of them.
type Sessions interface {
	Create(ctx context.Context, s *NewSession) error
	// ByRefreshForUpdate finds the session whose current refresh hash is hash and locks it.
	ByRefreshForUpdate(ctx context.Context, hash []byte) (s *SessionRecord, found bool, err error)
	// ForUpdate locks the session id.
	ForUpdate(ctx context.Context, id uuid.UUID) (s *SessionRecord, found bool, err error)
	// History finds a rotated refresh hash.
	History(ctx context.Context, hash []byte) (h *RefreshHistory, found bool, err error)
	// Rotate moves the current hash oldHash of session id to the history and sets newHash.
	Rotate(ctx context.Context, id uuid.UUID, oldHash, newHash []byte, now time.Time) error
	// Revoke revokes the active session id of userID.
	Revoke(ctx context.Context, userID, id uuid.UUID) (found bool, err error)
	// RevokeAll revokes the active sessions of userID except keep and returns their ids.
	RevokeAll(ctx context.Context, userID uuid.UUID, keep *uuid.UUID) ([]uuid.UUID, error)
	ListActive(ctx context.Context, userID uuid.UUID) ([]entity.Session, error)
}

// Token purposes.
const (
	TokenEmailVerify   = "email_verify"
	TokenPasswordReset = "password_reset"
)

// Tokens stores the hashes of the one-time tokens.
type Tokens interface {
	// Issue stores a token hash for userID and invalidates the unused tokens of the purpose.
	Issue(ctx context.Context, userID uuid.UUID, purpose string, hash []byte, expires time.Time) error
	// Consume marks a valid unused token used and returns its user.
	Consume(ctx context.Context, purpose string, hash []byte) (userID uuid.UUID, found bool, err error)
}

// Mail kinds.
const (
	MailVerifyEmail   = "verify_email"
	MailResetPassword = "reset_password"
)

// MailJob asks the worker to issue a one-time token for a user and mail it.
type MailJob struct {
	Kind   string
	UserID uuid.UUID
	Locale string
}

// MailQueue enqueues mail jobs in the request transaction.
type MailQueue interface {
	Enqueue(ctx context.Context, job *MailJob) error
}

// Mail is a rendered message.
type Mail struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer sends rendered messages.
type Mailer interface {
	Send(ctx context.Context, m *Mail) error
}

// RefreshGrace keeps the sealed token pair of a rotated refresh token for the grace period.
type RefreshGrace interface {
	Put(ctx context.Context, key string, sealed []byte, ttl time.Duration) error
	Get(ctx context.Context, key string) (sealed []byte, found bool, err error)
}
