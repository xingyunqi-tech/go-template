// Package repo declares the storage ports the use cases depend on.
package repo

import (
	"context"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

// Optional is a value a request may leave out: Set is false for undefined, Value nil for null.
type Optional[T any] struct {
	Set   bool
	Value *T
}

// Some returns a set, non-null Optional.
func Some[T any](v T) Optional[T] {
	return Optional[T]{Set: true, Value: &v}
}

// Null returns a set Optional holding null.
func Null[T any]() Optional[T] {
	return Optional[T]{Set: true, Value: nil}
}

// UserQuery selects one active user by a unique key; exactly one field is set.
type UserQuery struct {
	ID           *int
	Email        *string
	Phone        *string
	WechatOpenID *string
	AlipayOpenID *string
}

// NewUser is the row a user is created from.
type NewUser struct {
	FirstName     *string
	LastName      *string
	Email         *string
	Phone         *string
	Nickname      *string
	Gender        *int
	Birthday      *time.Time
	Password      *string
	Provider      string
	WechatOpenID  *string
	WechatUnionID *string
	AlipayOpenID  *string
	PhotoID       *uuid.UUID
	RoleID        *int
	StatusID      *int
}

// UserChanges is a partial update; RoleID and StatusID change only when non-nil.
type UserChanges struct {
	FirstName Optional[string]
	LastName  Optional[string]
	Email     Optional[string]
	Phone     Optional[string]
	Nickname  Optional[string]
	Gender    Optional[int]
	Birthday  Optional[time.Time]
	Password  Optional[string]
	Provider  Optional[string]
	PhotoID   Optional[uuid.UUID]
	RoleID    *int
	StatusID  *int
}

// SortField orders a user list by a user property.
type SortField struct {
	Property   string
	Descending bool
}

// UserList selects a page of active users.
type UserList struct {
	RoleIDs []int
	Sort    []SortField
	Offset  int64
	Limit   int64
}

// Users stores users.
type Users interface {
	Get(ctx context.Context, q UserQuery) (*entity.User, error)
	Create(ctx context.Context, u *NewUser) (int, error)
	Update(ctx context.Context, id int, c *UserChanges) (bool, error)
	ReplacePassword(ctx context.Context, id int, old *string, hash string) (bool, error)
	RehashPassword(ctx context.Context, id int, old, hash string) error
	Remove(ctx context.Context, id int) error
	List(ctx context.Context, q *UserList) ([]entity.User, error)
	CountByRole(ctx context.Context, roleID int) (int, error)
}

// SessionAuth is what the access-token guard reads in one query.
type SessionAuth struct {
	SessionID int
	UserID    int
	StatusID  int
	RoleID    *int
}

// Sessions stores sessions.
type Sessions interface {
	Get(ctx context.Context, id int) (*entity.Session, error)
	Create(ctx context.Context, userID int, hash string, userAgent, ip *string) (*entity.Session, error)
	Rotate(ctx context.Context, id int, current, next string) (bool, error)
	ListActive(ctx context.Context, userID int) ([]entity.Session, error)
	// Authenticate loads the session and its user and touches lastActiveAt on one connection.
	Authenticate(ctx context.Context, id int) (*SessionAuth, error)
	DeleteForUser(ctx context.Context, id, userID int) (bool, error)
	Delete(ctx context.Context, id int) error
	DeleteAllForUser(ctx context.Context, userID int, except *int) ([]int, error)
}

// Files reads uploaded files.
type Files interface {
	GetMany(ctx context.Context, ids []uuid.UUID) ([]entity.File, error)
	Create(ctx context.Context, f *entity.File) (*entity.File, error)
	// Get and GetByPath return nil when the file does not exist.
	Get(ctx context.Context, id uuid.UUID) (*entity.File, error)
	GetByPath(ctx context.Context, path string) (*entity.File, error)
	// ListByOwner returns up to limit files of the owner after afterID, by id.
	ListByOwner(ctx context.Context, ownerID int, afterID *uuid.UUID, limit int) ([]entity.File, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// Permissions reads role_permission.
type Permissions interface {
	RolePermissions(ctx context.Context) (map[int][]string, error)
}

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

// DatabaseHealth reports PostgreSQL reachability and server connection usage.
type DatabaseHealth interface {
	Ping(ctx context.Context) error
	ConnectionStats(ctx context.Context) (active, maximum int, err error)
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

// Jobs enqueues background jobs, inside the request transaction when there is one.
type Jobs interface {
	EnqueueMail(ctx context.Context, job entity.MailJob) error
	// EnqueueAccountCleanup is a no-op while a cleanup of the same user is pending or running.
	EnqueueAccountCleanup(ctx context.Context, userID int) error
}

// Alerts stores admin alerts outside any request transaction.
type Alerts interface {
	// CreateIfNoOpen inserts the alert unless one with the same dedup key is unacknowledged.
	CreateIfNoOpen(ctx context.Context, a *entity.Alert) (id int, created bool, err error)
	// AdminIDs lists up to limit admin users.
	AdminIDs(ctx context.Context, limit int) ([]int, error)
	// Notify creates an in-app notification unless the user turned in-app off for its category.
	Notify(ctx context.Context, n *entity.Notification) error
}

// AccountCleanups tracks the cleanup of removed accounts.
type AccountCleanups interface {
	MarkCompleted(ctx context.Context, userID int) error
	// PendingUserIDs lists users removed before deletedBefore without a completed cleanup, by id.
	PendingUserIDs(ctx context.Context, deletedBefore time.Time, limit int) ([]int, error)
	// DetachPhoto clears the avatar reference of a user, removed or not.
	DetachPhoto(ctx context.Context, userID int) error
}

// Maintenance runs the periodic cleanup statements.
type Maintenance interface {
	// PurgeDeletedSessions hard-deletes sessions soft-deleted more than retention ago.
	PurgeDeletedSessions(ctx context.Context, retentionDays int) (int64, error)
	// SoftDeleteReadNotifications soft-deletes notifications read more than retention ago.
	SoftDeleteReadNotifications(ctx context.Context, retentionDays int) (int64, error)
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

// AuditLog is an audit_logs row.
type AuditLog struct {
	UserID     *string
	Action     string
	EntityType string
	EntityID   string
	OldValue   map[string]any
	NewValue   map[string]any
	IPAddress  *string
	UserAgent  *string
	RequestID  *string
}

// Audits stores audit logs outside any request transaction.
type Audits interface {
	Create(ctx context.Context, l *AuditLog) error
}
