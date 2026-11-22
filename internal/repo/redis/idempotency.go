package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_claimAttempts = 2
	_purgeInterval = time.Minute
)

var errClaimContended = errors.New("redis: idempotency: key changed during claim")

// _ownedScript runs ARGV[2] on KEYS[1] while ARGV[1] owns its unfinished claim: "set" stores
// ARGV[3] for ARGV[4] milliseconds, "del" deletes the key. It returns 1 when it ran.
const _ownedScript = `
local raw = redis.call("GET", KEYS[1])
if not raw then return 0 end
local rec = cjson.decode(raw)
if rec.done or rec.owner ~= ARGV[1] then return 0 end
if ARGV[2] == "set" then
  redis.call("SET", KEYS[1], ARGV[3], "PX", ARGV[4])
else
  redis.call("DEL", KEYS[1])
end
return 1
`

// Idempotency stores idempotency keys in Redis, or in process without a client.
type Idempotency struct {
	client *goredis.Client
	owned  *goredis.Script
	now    func() time.Time

	mu     sync.Mutex
	local  map[string]idempotencyEntry
	purged time.Time
}

type idempotencyEntry struct {
	record  repo.IdempotencyRecord
	expires time.Time
}

var _ repo.IdempotencyStore = (*Idempotency)(nil)

// NewIdempotency returns the store; client may be nil.
func NewIdempotency(client *goredis.Client) *Idempotency {
	return &Idempotency{
		client: client, owned: goredis.NewScript(_ownedScript), now: time.Now, mu: sync.Mutex{}, local: map[string]idempotencyEntry{}, purged: time.Time{},
	}
}

// Begin implements repo.IdempotencyStore.
func (s *Idempotency) Begin(ctx context.Context, key, hash, owner string, ttl time.Duration) (*repo.IdempotencyRecord, error) {
	claim := repo.IdempotencyRecord{Hash: hash, Owner: owner, Done: false, Status: 0, ContentType: "", Body: nil}

	if s.client == nil {
		return s.beginLocal(key, &claim, ttl), nil
	}

	raw, err := json.Marshal(claim)
	if err != nil {
		return nil, fmt.Errorf("redis: idempotency: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	stored, err := s.claim(cmdCtx, key, raw, ttl)
	if err != nil || stored == nil {
		return nil, err
	}

	var rec repo.IdempotencyRecord
	if err := json.Unmarshal(stored, &rec); err != nil {
		return nil, fmt.Errorf("redis: idempotency: %w", err)
	}

	return &rec, nil
}

func (s *Idempotency) beginLocal(key string, claim *repo.IdempotencyRecord, ttl time.Duration) *repo.IdempotencyRecord {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if e, ok := s.local[key]; ok && now.Before(e.expires) {
		rec := e.record

		return &rec
	}

	if now.Sub(s.purged) >= _purgeInterval {
		s.purged = now

		for k, e := range s.local {
			if !now.Before(e.expires) {
				delete(s.local, k)
			}
		}
	}

	s.local[key] = idempotencyEntry{record: *claim, expires: now.Add(ttl)}

	return nil
}

// claim sets key unless it exists and returns the stored value otherwise; nil means claimed.
func (s *Idempotency) claim(ctx context.Context, key string, raw []byte, ttl time.Duration) ([]byte, error) {
	for range _claimAttempts {
		claimed, err := s.client.SetNX(ctx, key, raw, ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("redis: idempotency: %w", err)
		}

		if claimed {
			return nil, nil //nolint:nilnil // claimed
		}

		stored, err := s.client.Get(ctx, key).Bytes()

		switch {
		case errors.Is(err, goredis.Nil):
			continue
		case err != nil:
			return nil, fmt.Errorf("redis: idempotency: %w", err)
		}

		return stored, nil
	}

	return nil, errClaimContended
}

// owns reports whether owner holds the unfinished claim of a local entry; s.mu is held.
func (s *Idempotency) owns(key, owner string) bool {
	e, ok := s.local[key]

	return ok && !e.record.Done && e.record.Owner == owner && s.now().Before(e.expires)
}

// Complete implements repo.IdempotencyStore.
func (s *Idempotency) Complete(ctx context.Context, key, owner string, rec *repo.IdempotencyRecord, ttl time.Duration) error {
	if s.client == nil {
		s.mu.Lock()
		defer s.mu.Unlock()

		if !s.owns(key, owner) {
			return repo.ErrIdempotencyClaimLost
		}

		s.local[key] = idempotencyEntry{record: *rec, expires: s.now().Add(ttl)}

		return nil
	}

	raw, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("redis: idempotency: %w", err)
	}

	return s.runOwned(ctx, key, owner, "set", string(raw), ttl.Milliseconds())
}

// Release implements repo.IdempotencyStore.
func (s *Idempotency) Release(ctx context.Context, key, owner string) error {
	if s.client == nil {
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.owns(key, owner) {
			delete(s.local, key)
		}

		return nil
	}

	err := s.runOwned(ctx, key, owner, "del")
	if errors.Is(err, repo.ErrIdempotencyClaimLost) {
		return nil
	}

	return err
}

func (s *Idempotency) runOwned(ctx context.Context, key, owner string, args ...any) error {
	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	ran, err := s.owned.Run(cmdCtx, s.client, []string{key}, append([]any{owner}, args...)...).Int()
	if err != nil {
		return fmt.Errorf("redis: idempotency: %w", err)
	}

	if ran == 0 {
		return repo.ErrIdempotencyClaimLost
	}

	return nil
}
