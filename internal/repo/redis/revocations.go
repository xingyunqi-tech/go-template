package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"uuid"

	goredis "github.com/redis/go-redis/v9"
)

const _revokedPrefix = "auth:revoked:sid:"

// Revocations holds the sessions revoked before their access tokens expire, in the
// auth:revoked:sid:{sid} keys every service reads; without Redis only in this process.
type Revocations struct {
	client *goredis.Client
	now    func() time.Time

	mu    sync.Mutex
	local map[uuid.UUID]time.Time
}

// NewRevocations returns Revocations; client may be nil.
func NewRevocations(client *goredis.Client) *Revocations {
	return &Revocations{client: client, now: time.Now, mu: sync.Mutex{}, local: map[uuid.UUID]time.Time{}}
}

// Revoke marks sid revoked for ttl.
func (r *Revocations) Revoke(ctx context.Context, sid uuid.UUID, ttl time.Duration) error {
	if r.client == nil {
		r.mu.Lock()
		defer r.mu.Unlock()

		now := r.now()
		for k, until := range r.local {
			if !now.Before(until) {
				delete(r.local, k)
			}
		}

		r.local[sid] = now.Add(ttl)

		return nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	if err := r.client.Set(cmdCtx, _revokedPrefix+sid.String(), "1", ttl).Err(); err != nil {
		return fmt.Errorf("redis: revoke: %w", err)
	}

	return nil
}

// IsRevoked reports whether sid is revoked; the error is a failed Redis call.
func (r *Revocations) IsRevoked(ctx context.Context, sid uuid.UUID) (bool, error) {
	if r.client == nil {
		r.mu.Lock()
		defer r.mu.Unlock()

		until, ok := r.local[sid]

		return ok && r.now().Before(until), nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	err := r.client.Get(cmdCtx, _revokedPrefix+sid.String()).Err()

	switch {
	case errors.Is(err, goredis.Nil):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("redis: revocation: %w", err)
	default:
		return true, nil
	}
}
