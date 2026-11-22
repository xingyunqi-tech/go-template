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
