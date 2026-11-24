// Package ratelimit is the GCRA rate limiter of platform conventions §7: rules of a rate and a
// burst, keys rl:{rule}:{subject} holding the theoretical arrival time (TAT) in Unix
// milliseconds, evaluated by a Redis Lua script that reads the clock with TIME, or in process
// without Redis.
package ratelimit

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	_commandTimeout = 500 * time.Millisecond
	_memoryMaxKeys  = 10_000
	_replyFields    = 4
	_msPerSecond    = 1000
)

// script returns {allowed, remaining, reset ms, retry ms}. ARGV: interval ms, burst, peek flag.
const script = `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local interval = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local tat = tonumber(redis.call('GET', KEYS[1]) or now)
if tat < now then
  tat = now
end
local tolerance = interval * burst
local next_tat = tat + interval
local allow_at = next_tat - tolerance
if now < allow_at then
  return {0, 0, tat - now, allow_at - now}
end
if ARGV[3] == '1' then
  return {1, math.floor((tolerance - (tat - now)) / interval), tat - now, 0}
end
redis.call('SET', KEYS[1], string.format('%d', next_tat), 'PX', next_tat - now)
return {1, math.floor((tolerance - (next_tat - now)) / interval), next_tat - now, 0}
`

// Rule is a rate of requests per period that allows bursts of Burst consecutive requests.
type Rule struct {
	Name   string
	Rate   int
	Period time.Duration
	Burst  int
}

// interval is the emission interval in whole milliseconds.
func (r Rule) interval() int64 {
	return max(1, r.Period.Milliseconds()/int64(r.Rate))
}

// Key is the Redis key of subject under the rule.
func (r Rule) Key(subject string) string {
	return "rl:" + r.Name + ":" + subject
}

// Decision is the outcome of one check.
type Decision struct {
	Allowed bool
	// Limit is the burst, sent as X-RateLimit-Limit.
	Limit int
	// Remaining is how many more requests may pass back to back now.
	Remaining int
	// ResetMillis is the time until the burst is full again.
	ResetMillis int64
	// RetryMillis is the wait before a rejected request may pass.
	RetryMillis int64
}

// ResetSeconds is X-RateLimit-Reset.
func (d Decision) ResetSeconds() int64 {
	return ceilSeconds(d.ResetMillis)
}

// RetryAfterSeconds is Retry-After, at least one second.
func (d Decision) RetryAfterSeconds() int64 {
	return max(1, ceilSeconds(d.RetryMillis))
}

func ceilSeconds(ms int64) int64 {
	if ms <= 0 {
		return 0
	}

	return (ms + _msPerSecond - 1) / _msPerSecond
}

// Gcra decides at now with the stored TAT (0 for a new key); next is the TAT to store when the
// request is allowed and consumed.
func Gcra(rule Rule, tat, now int64, peek bool) (d Decision, next int64) {
	interval := rule.interval()
	tolerance := interval * int64(rule.Burst)
	tat = max(tat, now)
	next = tat + interval

	if allowAt := next - tolerance; now < allowAt {
		return Decision{Allowed: false, Limit: rule.Burst, Remaining: 0, ResetMillis: tat - now, RetryMillis: allowAt - now}, tat
	}

	if peek {
		return Decision{
			Allowed: true, Limit: rule.Burst, Remaining: int((tolerance - (tat - now)) / interval), ResetMillis: tat - now, RetryMillis: 0,
		}, tat
	}

	return Decision{
		Allowed: true, Limit: rule.Burst, Remaining: int((tolerance - (next - now)) / interval), ResetMillis: next - now, RetryMillis: 0,
	}, next
}

// Limiter checks rules in Redis, or in process memory when it has no Redis client.
type Limiter struct {
	redis  *goredis.Client
	script *goredis.Script
	now    func() time.Time

	mu    sync.Mutex
	tat   map[string]*entry
	queue tatQueue
}

// entry is the theoretical arrival time of one key and its place in the queue.
type entry struct {
	key   string
	tat   int64
	index int
}

// tatQueue is a min-heap of entries by tat: the next key to expire comes first.
type tatQueue []*entry

func (q tatQueue) Len() int           { return len(q) }
func (q tatQueue) Less(i, j int) bool { return q[i].tat < q[j].tat }

func (q tatQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
	q[i].index, q[j].index = i, j
}

func (q *tatQueue) Push(x any) {
	e, _ := x.(*entry)
	e.index = len(*q)
	*q = append(*q, e)
}

func (q *tatQueue) Pop() any {
	old := *q
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*q = old[:len(old)-1]

	return e
}

// New returns a Limiter; client may be nil to keep the state in process.
func New(client *goredis.Client) *Limiter {
	return &Limiter{
		redis: client, script: goredis.NewScript(script), now: time.Now, mu: sync.Mutex{}, tat: map[string]*entry{}, queue: nil,
	}
}

var errReply = errors.New("ratelimit: unexpected script reply")

// Consume checks the rule for subject and counts the request when it is allowed; the error is a
// failed Redis call.
func (l *Limiter) Consume(ctx context.Context, rule Rule, subject string) (Decision, error) {
	return l.check(ctx, rule, subject, false)
}

// Peek checks the rule for subject without counting the request.
func (l *Limiter) Peek(ctx context.Context, rule Rule, subject string) (Decision, error) {
	return l.check(ctx, rule, subject, true)
}

// Reset forgets the requests counted for subject.
func (l *Limiter) Reset(ctx context.Context, rule Rule, subject string) error {
	key := rule.Key(subject)

	if l.redis == nil {
		l.mu.Lock()
		l.forget(key)
		l.mu.Unlock()

		return nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	if err := l.redis.Del(cmdCtx, key).Err(); err != nil {
		return fmt.Errorf("ratelimit: redis: %w", err)
	}

	return nil
}

func (l *Limiter) check(ctx context.Context, rule Rule, subject string, peek bool) (Decision, error) {
	if l.redis == nil {
		return l.checkMemory(rule, rule.Key(subject), peek), nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	flag := "0"
	if peek {
		flag = "1"
	}

	vals, err := l.script.Run(cmdCtx, l.redis, []string{rule.Key(subject)}, rule.interval(), rule.Burst, flag).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("ratelimit: redis: %w", err)
	}

	if len(vals) != _replyFields {
		return Decision{}, errReply
	}

	return Decision{
		Allowed: vals[0] == 1, Limit: rule.Burst, Remaining: int(vals[1]), ResetMillis: vals[2], RetryMillis: vals[3],
	}, nil
}

func (l *Limiter) checkMemory(rule Rule, key string, peek bool) Decision {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now().UnixMilli()

	var tat int64
	if e, ok := l.tat[key]; ok {
		tat = e.tat
	}

	d, next := Gcra(rule, tat, now, peek)
	if !d.Allowed || peek {
		return d
	}

	if e, ok := l.tat[key]; ok {
		e.tat = next
		heap.Fix(&l.queue, e.index)

		return d
	}

	l.evict(now)

	e := &entry{key: key, tat: next, index: 0}
	l.tat[key] = e
	heap.Push(&l.queue, e)

	return d
}

// evict drops the expired keys, then the keys expiring first while full.
func (l *Limiter) evict(now int64) {
	for l.queue.Len() > 0 && (l.queue[0].tat <= now || l.queue.Len() >= _memoryMaxKeys) {
		e, _ := heap.Pop(&l.queue).(*entry)
		delete(l.tat, e.key)
	}
}

func (l *Limiter) forget(key string) {
	if e, ok := l.tat[key]; ok {
		heap.Remove(&l.queue, e.index)
		delete(l.tat, key)
	}
}

// Tightest returns the decision with the fewest remaining requests, the one sent in the
// X-RateLimit-* headers when a request matches several rules.
func Tightest(decisions ...Decision) (Decision, bool) {
	if len(decisions) == 0 {
		return Decision{}, false
	}

	best := decisions[0]
	for _, d := range decisions[1:] {
		if d.Remaining < best.Remaining || (d.Remaining == best.Remaining && d.ResetMillis > best.ResetMillis) {
			best = d
		}
	}

	return best, true
}
