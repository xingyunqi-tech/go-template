// Package ratelimit is a GCRA rate limiter in integer microseconds: a Redis Lua script shared by
// every process, with an in-process fallback that runs the same algorithm.
package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	_microsPerMilli  = 1000
	_microsPerSecond = 1_000_000
	_commandTimeout  = 500 * time.Millisecond
	_memoryMaxKeys   = 10_000
)

// script stores the theoretical arrival time (TAT) as decimal milliseconds with three decimals
// and reads it back rounded to whole microseconds.
const script = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local period = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local interval = math.floor(period / limit)
local tat = now
local stored = redis.call('GET', KEYS[1])
if stored then
  tat = math.floor(tonumber(stored) * 1000 + 0.5)
end
if tat < now then
  tat = now
end
local wait = (tat - now) + interval - period
if wait > 0 then
  return {0, 0, tat - now, wait}
end
local new_tat = tat + interval
redis.call('SET', KEYS[1], string.format('%.3f', new_tat / 1000), 'PX', math.ceil((new_tat - now) / 1000))
return {1, math.floor((period - (new_tat - now)) / interval), new_tat - now, 0}
`

// Decision is the outcome of one request; durations are in microseconds.
type Decision struct {
	Allowed     bool
	Remaining   int64
	ResetMicros int64
	WaitMicros  int64
}

func ceilDiv(a, b int64) int64 {
	if a <= 0 {
		return 0
	}

	return (a + b - 1) / b
}

// ResetSeconds is X-RateLimit-Reset.
func (d Decision) ResetSeconds() int64 {
	return ceilDiv(d.ResetMicros, _microsPerSecond)
}

// RetryAfterSeconds is Retry-After, at least one second.
func (d Decision) RetryAfterSeconds() int64 {
	return max(1, ceilDiv(d.WaitMicros, _microsPerSecond))
}

// Gcra decides with the theoretical arrival time tat (0 for a new key) at now.
func Gcra(tat, now, periodMicros int64, limit int) (d Decision, newTAT int64) {
	interval := periodMicros / int64(limit)
	tat = max(tat, now)

	if wait := (tat - now) + interval - periodMicros; wait > 0 {
		return Decision{Allowed: false, Remaining: 0, ResetMicros: tat - now, WaitMicros: wait}, tat
	}

	next := tat + interval

	return Decision{
		Allowed: true, Remaining: (periodMicros - (next - now)) / interval, ResetMicros: next - now, WaitMicros: 0,
	}, next
}

// FormatTAT writes a TAT in microseconds as decimal milliseconds with three decimals.
func FormatTAT(micros int64) string {
	return strconv.FormatFloat(float64(micros)/_microsPerMilli, 'f', 3, 64)
}

// ParseTAT reads decimal milliseconds rounded to whole microseconds.
func ParseTAT(s string) (int64, error) {
	ms, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("ratelimit: tat: %w", err)
	}

	return int64(math.Round(ms * _microsPerMilli)), nil
}

// Memory is the in-process store, bounded to 10000 keys.
type Memory struct {
	mu  sync.Mutex
	tat map[string]int64
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{mu: sync.Mutex{}, tat: map[string]int64{}}
}

// Consume decides for key at now (microseconds).
func (m *Memory) Consume(key string, periodMicros int64, limit int, now int64) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()

	d, next := Gcra(m.tat[key], now, periodMicros, limit)
	if !d.Allowed {
		return d
	}

	if _, ok := m.tat[key]; !ok && len(m.tat) >= _memoryMaxKeys {
		m.evict(now)
	}

	m.tat[key] = next

	return d
}

// evict drops expired keys, then the key expiring first while still full.
func (m *Memory) evict(now int64) {
	for k, v := range m.tat {
		if v <= now {
			delete(m.tat, k)
		}
	}

	for len(m.tat) >= _memoryMaxKeys {
		oldest, first := "", int64(math.MaxInt64)

		for k, v := range m.tat {
			if v < first {
				oldest, first = k, v
			}
		}

		delete(m.tat, oldest)
	}
}

// Observer is told when Redis fails and when it works again.
type Observer interface {
	Fallback(ctx context.Context, err error, first bool)
	Recovered(ctx context.Context)
}

// Limiter consumes from Redis and falls back to Memory when Redis is disabled or fails.
type Limiter struct {
	redis    *goredis.Client
	memory   *Memory
	observer Observer
	script   *goredis.Script
	now      func() time.Time

	mu       sync.Mutex
	degraded bool
}

// New returns a Limiter; client may be nil to use memory only.
func New(client *goredis.Client, observer Observer) *Limiter {
	return &Limiter{
		redis: client, memory: NewMemory(), observer: observer, script: goredis.NewScript(script), now: time.Now,
		mu: sync.Mutex{}, degraded: false,
	}
}

var errReply = errors.New("ratelimit: unexpected script reply")

// Consume decides for key with limit requests per period.
func (l *Limiter) Consume(ctx context.Context, key string, period time.Duration, limit int) Decision {
	periodMicros := period.Microseconds()

	if l.redis != nil {
		d, err := l.consumeRedis(ctx, key, periodMicros, limit)
		if err == nil {
			l.setDegraded(ctx, false, nil)

			return d
		}

		l.setDegraded(ctx, true, err)
	}

	return l.memory.Consume(key, periodMicros, limit, l.now().UnixMicro())
}

func (l *Limiter) consumeRedis(ctx context.Context, key string, periodMicros int64, limit int) (Decision, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	vals, err := l.script.Run(cmdCtx, l.redis, []string{key}, periodMicros, limit).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("ratelimit: redis: %w", err)
	}

	const fields = 4
	if len(vals) != fields {
		return Decision{}, errReply
	}

	return Decision{Allowed: vals[0] == 1, Remaining: vals[1], ResetMicros: vals[2], WaitMicros: vals[3]}, nil
}

func (l *Limiter) setDegraded(ctx context.Context, degraded bool, err error) {
	l.mu.Lock()
	changed := l.degraded != degraded
	l.degraded = degraded
	l.mu.Unlock()

	if l.observer == nil {
		return
	}

	switch {
	case degraded:
		l.observer.Fallback(ctx, err, changed)
	case changed:
		l.observer.Recovered(ctx)
	}
}
