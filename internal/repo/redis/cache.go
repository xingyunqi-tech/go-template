// Package redis implements the cache port compatible with nestjs cache-manager + Keyv: keys are
// "cache::<key>" and values {"value": ..., "expires": <ms>}.
package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_namespace      = "cache"
	_memoryMaxItems = 10_000
	_commandTimeout = 500 * time.Millisecond
)

type envelope struct {
	Value   json.RawMessage `json:"value"`
	Expires int64           `json:"expires"`
}

// Cache stores values in Redis, falling back to process memory when Redis is disabled or fails.
type Cache struct {
	client *goredis.Client
	memory *Memory
	log    *slog.Logger
	now    func() time.Time
}

// NewCache returns a Cache; client may be nil to use memory only.
func NewCache(client *goredis.Client, log *slog.Logger) *Cache {
	return &Cache{client: client, memory: NewMemory(), log: log, now: time.Now}
}

var _ repo.Cache = (*Cache)(nil)

func key(k string) string {
	return _namespace + "::" + k
}

func (c *Cache) fallback(ctx context.Context, op string, err error) {
	c.log.WarnContext(ctx, "redis cache unavailable, using memory", "op", op, "error", err.Error())
}

// Get decodes the cached value into dst and reports whether it was present.
func (c *Cache) Get(ctx context.Context, k string, dst any) (bool, error) {
	if c.client == nil {
		return c.memory.Get(ctx, k, dst)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	raw, err := c.client.Get(cmdCtx, key(k)).Bytes()

	switch {
	case errors.Is(err, goredis.Nil):
		return false, nil
	case err != nil:
		c.fallback(ctx, "get", err)

		return c.memory.Get(ctx, k, dst)
	}

	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return false, nil //nolint:nilerr // unreadable entries count as misses
	}

	if e.Expires != 0 && e.Expires <= c.now().UnixMilli() {
		return false, nil
	}

	if err := json.Unmarshal(e.Value, dst); err != nil {
		return false, nil //nolint:nilerr // unreadable entries count as misses
	}

	return true, nil
}

// Set stores value for ttl.
func (c *Cache) Set(ctx context.Context, k string, value any, ttl time.Duration) error {
	if c.client == nil {
		return c.memory.Set(ctx, k, value, ttl)
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache: encode: %w", err)
	}

	payload, err := json.Marshal(envelope{Value: raw, Expires: c.now().Add(ttl).UnixMilli()})
	if err != nil {
		return fmt.Errorf("cache: encode: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	if err := c.client.Set(cmdCtx, key(k), payload, ttl).Err(); err != nil {
		c.fallback(ctx, "set", err)

		return c.memory.Set(ctx, k, value, ttl)
	}

	return nil
}

// Delete removes the key from Redis and from the fallback memory.
func (c *Cache) Delete(ctx context.Context, k string) error {
	_ = c.memory.Delete(ctx, k)

	if c.client == nil {
		return nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	if err := c.client.Del(cmdCtx, key(k)).Err(); err != nil {
		c.fallback(ctx, "delete", err)
	}

	return nil
}

type memoryItem struct {
	value   []byte
	expires time.Time
}

// Memory is a bounded in-process cache.
type Memory struct {
	mu    sync.Mutex
	items map[string]memoryItem
	order []string
	now   func() time.Time
}

// NewMemory returns an empty Memory.
func NewMemory() *Memory {
	return &Memory{mu: sync.Mutex{}, items: map[string]memoryItem{}, order: nil, now: time.Now}
}

var _ repo.Cache = (*Memory)(nil)

// Get decodes the cached value into dst.
func (m *Memory) Get(_ context.Context, k string, dst any) (bool, error) {
	m.mu.Lock()
	item, ok := m.items[k]

	if ok && !item.expires.After(m.now()) {
		delete(m.items, k)

		ok = false
	}
	m.mu.Unlock()

	if !ok {
		return false, nil
	}

	if err := json.Unmarshal(item.value, dst); err != nil {
		return false, fmt.Errorf("cache: decode: %w", err)
	}

	return true, nil
}

// Set stores value for ttl, evicting expired and then the oldest entries when full.
func (m *Memory) Set(_ context.Context, k string, value any, ttl time.Duration) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache: encode: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()

	if _, exists := m.items[k]; !exists {
		if len(m.items) >= _memoryMaxItems {
			m.evict(now)
		}

		m.order = append(m.order, k)

		if len(m.order) > 2*_memoryMaxItems {
			m.compact()
		}
	}

	m.items[k] = memoryItem{value: raw, expires: now.Add(ttl)}

	return nil
}

func (m *Memory) evict(now time.Time) {
	kept := m.order[:0]

	for _, k := range m.order {
		if item, ok := m.items[k]; ok && item.expires.After(now) {
			kept = append(kept, k)
		} else {
			delete(m.items, k)
		}
	}

	m.order = kept

	for len(m.items) >= _memoryMaxItems && len(m.order) > 0 {
		delete(m.items, m.order[0])
		m.order = m.order[1:]
	}
}

// compact drops keys that are no longer stored from the insertion order.
func (m *Memory) compact() {
	seen := make(map[string]bool, len(m.items))
	kept := m.order[:0]

	for _, k := range m.order {
		if _, ok := m.items[k]; ok && !seen[k] {
			seen[k] = true
			kept = append(kept, k)
		}
	}

	m.order = kept
}

// Delete removes the key.
func (m *Memory) Delete(_ context.Context, k string) error {
	m.mu.Lock()
	delete(m.items, k)
	m.mu.Unlock()

	return nil
}
