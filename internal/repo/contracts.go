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
	// EnqueueNotification queues a job on the notification queue.
	EnqueueNotification(ctx context.Context, job entity.NotificationJob) error
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
	// UserState reports whether the user exists, removed or not, and whether it is removed.
	UserState(ctx context.Context, userID int) (exists, removed bool, err error)
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

// NotificationFilter narrows the notifications of a user; nil fields do not filter.
type NotificationFilter struct {
	Category *string
	IsRead   *bool
}

// NewNotification is the row a notification is created from; Metadata is nil for SQL null.
type NewNotification struct {
	UserID   int
	Type     string
	Category string
	Title    string
	Content  string
	Metadata []byte
	Channels []string
}

// InAppBatch is one in-app notification written to several users.
type InAppBatch struct {
	UserIDs  []int
	Type     string
	Category string
	Title    string
	Content  string
	Metadata []byte
}

// NotificationFlags are the channel switches of a notification setting.
type NotificationFlags struct {
	InApp bool
	Email bool
	SMS   bool
	Push  bool
}

// Notifications stores notifications and notification settings. A user sees the notifications
// that are not deleted and include the IN_APP channel; the lookups return nil when nothing matches.
type Notifications interface {
	List(ctx context.Context, userID int, f NotificationFilter, offset, limit int) ([]entity.NotificationRecord, error)
	Count(ctx context.Context, userID int, f NotificationFilter) (int, error)
	Get(ctx context.Context, id, userID int) (*entity.NotificationRecord, error)
	MarkRead(ctx context.Context, id, userID int, at time.Time) (*entity.NotificationRecord, error)
	MarkAllRead(ctx context.Context, userID int, at time.Time) error
	// SoftDelete reports whether a visible notification was deleted.
	SoftDelete(ctx context.Context, id, userID int) (bool, error)
	Create(ctx context.Context, n *NewNotification) (*entity.NotificationRecord, error)
	// CreateInApp skips the users who turned in-app off for the category and returns the rows written.
	CreateInApp(ctx context.Context, b *InAppBatch) (int, error)
	// RecordChannel merges {channel: result} into sentChannels of a notification that is not deleted.
	RecordChannel(ctx context.Context, id int, channel string, result map[string]any) error
	Setting(ctx context.Context, userID int, category string) (*entity.NotificationSetting, error)
	Settings(ctx context.Context, userID int) ([]entity.NotificationSetting, error)
	CreateSetting(ctx context.Context, userID int, category string, f NotificationFlags) (*entity.NotificationSetting, error)
	UpdateSetting(ctx context.Context, id int, f NotificationFlags) (*entity.NotificationSetting, error)
	// InitSettings creates the missing settings of every category with all channels on.
	InitSettings(ctx context.Context, userID int, categories []string) error
	// RemoveUser hard-deletes every notification and setting of a user.
	RemoveUser(ctx context.Context, userID int) error
}

// UserBindings attaches a phone number or a WeChat account to an active user that has none.
type UserBindings interface {
	BindPhoneIfEmpty(ctx context.Context, id int, phone string) (bool, error)
	// BindWechatIfEmpty sets the open id, and the union id when unionID is not nil.
	BindWechatIfEmpty(ctx context.Context, id int, openID string, unionID *string) (bool, error)
}

// NewAddress is the row an address is created from; it starts as not default.
type NewAddress struct {
	UserID       int
	Name         string
	Phone        string
	ProvinceCode *string
	CityCode     *string
	DistrictCode *string
	Province     string
	City         string
	District     string
	Address      string
}

// AddressChanges is a partial address update.
type AddressChanges struct {
	Name         Optional[string]
	Phone        Optional[string]
	ProvinceCode Optional[string]
	CityCode     Optional[string]
	DistrictCode Optional[string]
	Province     Optional[string]
	City         Optional[string]
	District     Optional[string]
	Address      Optional[string]
	IsDefault    Optional[bool]
}

// AddressFilter narrows the active addresses of a user; empty fields do not filter.
type AddressFilter struct {
	Province     string
	City         string
	ProvinceCode string
	CityCode     string
	DistrictCode string
}

// AddressList selects a page of the active addresses of a user.
type AddressList struct {
	UserID int
	Filter AddressFilter
	Sort   []SortField
	Offset int64
	Limit  int64
}

// Addresses stores shipping addresses; reads skip soft-deleted rows.
type Addresses interface {
	// CreateWithinLimit inserts the address while the user has fewer than limit active ones and
	// returns it with the count before the insert; created is nil at the limit.
	CreateWithinLimit(ctx context.Context, a *NewAddress, limit int) (created *entity.Address, previous int, err error)
	List(ctx context.Context, q *AddressList) ([]entity.Address, error)
	Count(ctx context.Context, userID int, f *AddressFilter) (int, error)
	// Get and GetDefault return nil when the user has no such address.
	Get(ctx context.Context, id, userID int) (*entity.Address, error)
	GetDefault(ctx context.Context, userID int) (*entity.Address, error)
	// Update applies c and returns the address, or nil when it is not active.
	Update(ctx context.Context, id int, c *AddressChanges) (*entity.Address, error)
	// MarkDefault makes the address the only default one of the user.
	MarkDefault(ctx context.Context, id, userID int) error
	SoftDelete(ctx context.Context, id int) error
	RemoveByUser(ctx context.Context, userID int) error
	Regions(ctx context.Context, codes []string) ([]entity.Region, error)
}

// NewFavorite is the row a favorite is created from.
type NewFavorite struct {
	UserID     int
	TargetType string
	TargetID   string
	Title      *string
	Image      *string
	Extra      *string
	SubjectID  *uuid.UUID
}

// FavoriteFilter narrows the favorites of a user; empty fields do not filter.
type FavoriteFilter struct {
	UserID     int
	TargetType string
	SubjectID  *uuid.UUID
}

// Favorites stores favorites.
type Favorites interface {
	Create(ctx context.Context, f *NewFavorite) (*entity.Favorite, error)
	// List returns a page ordered by createdAt descending.
	List(ctx context.Context, f FavoriteFilter, offset, limit int64) ([]entity.Favorite, error)
	Count(ctx context.Context, f FavoriteFilter) (int, error)
	// Get and GetByTarget return nil when the favorite does not exist.
	Get(ctx context.Context, id int) (*entity.Favorite, error)
	GetByTarget(ctx context.Context, userID int, targetType, targetID string) (*entity.Favorite, error)
	Delete(ctx context.Context, id int) error
	RemoveByUser(ctx context.Context, userID int) error
}

// NewFeedback is the row feedback is created from; nil Images is stored as NULL.
type NewFeedback struct {
	UserID  int
	Type    string
	Content string
	Images  []byte
	Contact *string
}

// FeedbackFilter selects feedback of a user with one of Types; an empty Status does not filter.
type FeedbackFilter struct {
	UserID int
	Types  []string
	Status string
}

// Feedbacks stores feedback.
type Feedbacks interface {
	Create(ctx context.Context, f *NewFeedback) (*entity.Feedback, error)
	// Get returns nil when the feedback does not exist.
	Get(ctx context.Context, id int) (*entity.Feedback, error)
	// List returns a page ordered by createdAt and id descending.
	List(ctx context.Context, f *FeedbackFilter, offset, limit int64) ([]entity.Feedback, error)
	Count(ctx context.Context, f *FeedbackFilter) (int, error)
	// AnonymizeByUser clears the user, contact and images of the user's feedback.
	AnonymizeByUser(ctx context.Context, userID int) error
}

// Configs stores config entries.
type Configs interface {
	// Get returns nil when the key does not exist.
	Get(ctx context.Context, key string) (*entity.Config, error)
	// Upsert creates the entry (isPublic nil means false) or updates it when value or isPublic
	// changes (isPublic nil keeps it); an unchanged entry keeps its updatedAt.
	Upsert(ctx context.Context, key string, value []byte, isPublic *bool) (*entity.Config, error)
}

// Regions reads administrative divisions, ordered by sort and code unless stated otherwise.
type Regions interface {
	// GetByCode returns nil when the code does not exist.
	GetByCode(ctx context.Context, code string) (*entity.Region, error)
	ListByLevel(ctx context.Context, level int) ([]entity.Region, error)
	ListByParent(ctx context.Context, parentCode string) ([]entity.Region, error)
	// ListAll orders by level, sort and code.
	ListAll(ctx context.Context) ([]entity.Region, error)
	// SearchByName matches names containing keyword case-insensitively, by level and sort.
	SearchByName(ctx context.Context, keyword string, limit int) ([]entity.Region, error)
}

// Consents stores agreement consents.
type Consents interface {
	// InsertIfAbsent keeps the first record of the same user, type and version.
	InsertIfAbsent(ctx context.Context, c *entity.UserConsent) error
	// Latest returns the most recent consent of each type.
	Latest(ctx context.Context, userID int) ([]entity.UserConsent, error)
	// Get returns nil when the user has not agreed to that version.
	Get(ctx context.Context, userID int, consentType, version string) (*entity.UserConsent, error)
	Exists(ctx context.Context, userID int, consentType, version string) (bool, error)
}
