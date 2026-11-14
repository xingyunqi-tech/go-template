package redis

import (
	"context"
	"log/slog"
	"sync"
	"time"
	"uuid"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_lockPrefix         = "lock:"
	_lockCommandTimeout = time.Second
	_releaseScript      = `if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) else return 0 end`
)

type localLock struct {
	token   string
	expires time.Time
}

// Locks implements repo.Locks like nestjs LockService: SET NX PX on lock:<key>, released by a
// token compare script; without Redis, or when a command fails, the lock holds in this process.
type Locks struct {
	client  *goredis.Client
	release *goredis.Script
	log     *slog.Logger
	now     func() time.Time

	mu    sync.Mutex
	local map[string]localLock
}

// NewLocks returns Locks; client may be nil to lock in process only.
func NewLocks(client *goredis.Client, log *slog.Logger) *Locks {
	return &Locks{
		client: client, release: goredis.NewScript(_releaseScript), log: log, now: time.Now,
		mu: sync.Mutex{}, local: map[string]localLock{},
	}
}

var _ repo.Locks = (*Locks)(nil)

// TryAcquire claims key for ttl.
func (l *Locks) TryAcquire(ctx context.Context, key string, ttl time.Duration) (string, bool) {
	token := uuid.New().String()

	if l.client != nil {
		cmdCtx, cancel := context.WithTimeout(ctx, _lockCommandTimeout)
		ok, err := l.client.SetNX(cmdCtx, _lockPrefix+key, token, ttl).Result()

		cancel()

		if err == nil {
			return token, ok
		}

		l.log.WarnContext(ctx, "redis lock unavailable, using local lock", "key", key, "error", err.Error())
	}

	return token, l.tryLocal(key, token, ttl)
}

func (l *Locks) tryLocal(key, token string, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if held, ok := l.local[key]; ok && now.Before(held.expires) {
		return false
	}

	for k, v := range l.local {
		if !now.Before(v.expires) {
			delete(l.local, k)
		}
	}

	l.local[key] = localLock{token: token, expires: now.Add(ttl)}

	return true
}

// Release frees key while token still holds it.
func (l *Locks) Release(ctx context.Context, key, token string) {
	l.mu.Lock()
	if held, ok := l.local[key]; ok && held.token == token {
		delete(l.local, key)
	}
	l.mu.Unlock()

	if l.client == nil {
		return
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _lockCommandTimeout)
	defer cancel()

	if err := l.release.Run(cmdCtx, l.client, []string{_lockPrefix + key}, token).Err(); err != nil {
		l.log.WarnContext(ctx, "failed to release lock", "key", key, "error", err.Error())
	}
}
