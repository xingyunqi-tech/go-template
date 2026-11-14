// Package sms sends and verifies the one-time SMS codes stored as sms:code:{type}:{phone}.
package sms

import (
	"context"
	"crypto/subtle"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	// MaxAttempts is how many wrong codes are accepted before the code is discarded.
	MaxAttempts = 5
	// CodeTTL is how long a sent code stays valid.
	CodeTTL = 300 * time.Second

	_lockStripes = 64
)

type stored struct {
	Code      string `json:"code"`
	Attempts  int    `json:"attempts"`
	CreatedAt int64  `json:"createdAt"`
}

// Codes verifies SMS codes.
type Codes struct {
	cache       repo.Cache
	locks       [_lockStripes]sync.Mutex
	now         func() time.Time
	maxAttempts int
	ttl         time.Duration
}

// NewCodes returns Codes with the default attempt limit and lifetime.
func NewCodes(cache repo.Cache) *Codes {
	return newCodes(cache, MaxAttempts, CodeTTL)
}

func newCodes(cache repo.Cache, maxAttempts int, ttl time.Duration) *Codes {
	return &Codes{cache: cache, locks: [_lockStripes]sync.Mutex{}, now: time.Now, maxAttempts: maxAttempts, ttl: ttl}
}

func (c *Codes) lock(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))

	return &c.locks[h.Sum32()%_lockStripes]
}

// CacheKey is the cache key of the code sent to phone for purpose.
func CacheKey(phone, purpose string) string {
	return "sms:code:" + purpose + ":" + phone
}

// Verify consumes the code on success; a wrong code counts as an attempt.
func (c *Codes) Verify(ctx context.Context, phone, code, purpose string) (bool, error) {
	key := CacheKey(phone, purpose)

	mu := c.lock(key)

	mu.Lock()
	defer mu.Unlock()

	var s stored

	found, err := c.cache.Get(ctx, key, &s)
	if err != nil {
		return false, fmt.Errorf("sms: %w", err)
	}

	if !found {
		return false, nil
	}

	if s.Attempts >= c.maxAttempts {
		return false, c.delete(ctx, key)
	}

	if subtle.ConstantTimeCompare([]byte(s.Code), []byte(code)) != 1 {
		s.Attempts++

		remaining := c.ttl - c.now().Sub(time.UnixMilli(s.CreatedAt))
		if remaining > 0 {
			if err := c.cache.Set(ctx, key, s, remaining); err != nil {
				return false, fmt.Errorf("sms: %w", err)
			}
		}

		return false, nil
	}

	return true, c.delete(ctx, key)
}

func (c *Codes) delete(ctx context.Context, key string) error {
	if err := c.cache.Delete(ctx, key); err != nil {
		return fmt.Errorf("sms: %w", err)
	}

	return nil
}
