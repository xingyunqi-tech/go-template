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
