package redis

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// RefreshGrace keeps the sealed token pairs of rotated refresh tokens under
// {service}:auth:refresh:grace:, or in process without a client.
type RefreshGrace struct {
	client *goredis.Client
	prefix string
	now    func() time.Time

	mu    sync.Mutex
	local map[string]graceEntry
}

type graceEntry struct {
	sealed  []byte
	expires time.Time
}

var _ repo.RefreshGrace = (*RefreshGrace)(nil)

// NewRefreshGrace returns RefreshGrace; client may be nil.
func NewRefreshGrace(client *goredis.Client, service string) *RefreshGrace {
	return &RefreshGrace{
		client: client, prefix: service + ":auth:refresh:grace:", now: time.Now, mu: sync.Mutex{}, local: map[string]graceEntry{},
	}
}

// Put implements repo.RefreshGrace.
func (g *RefreshGrace) Put(ctx context.Context, key string, sealed []byte, ttl time.Duration) error {
	if g.client == nil {
		g.mu.Lock()
		defer g.mu.Unlock()

		now := g.now()
		for k, e := range g.local {
			if !now.Before(e.expires) {
				delete(g.local, k)
			}
		}

		g.local[key] = graceEntry{sealed: sealed, expires: now.Add(ttl)}

		return nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	if err := g.client.Set(cmdCtx, g.prefix+key, sealed, ttl).Err(); err != nil {
		return fmt.Errorf("redis: refresh grace: %w", err)
	}

	return nil
}

// Get implements repo.RefreshGrace.
func (g *RefreshGrace) Get(ctx context.Context, key string) (sealed []byte, found bool, err error) {
	if g.client == nil {
		g.mu.Lock()
		defer g.mu.Unlock()

		e, ok := g.local[key]
		if !ok || !g.now().Before(e.expires) {
			return nil, false, nil
		}

		return e.sealed, true, nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	sealed, err = g.client.Get(cmdCtx, g.prefix+key).Bytes()

	switch {
	case errors.Is(err, goredis.Nil):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("redis: refresh grace: %w", err)
	}

	return sealed, true, nil
}
