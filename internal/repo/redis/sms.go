package redis

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// _reserveScript: KEYS lock, cooldown, daily; ARGV cooldown ms, daily max, daily ttl ms.
const _reserveScript = `
local lock = redis.call("PTTL", KEYS[1])
if lock > 0 then return {1, lock} end
local cool = redis.call("PTTL", KEYS[2])
if cool > 0 then return {2, cool} end
local n = tonumber(redis.call("GET", KEYS[3]) or "0")
if n >= tonumber(ARGV[2]) then return {2, redis.call("PTTL", KEYS[3])} end
redis.call("SET", KEYS[2], "1", "PX", ARGV[1])
if redis.call("INCR", KEYS[3]) == 1 then redis.call("PEXPIRE", KEYS[3], ARGV[3]) end
return {0, 0}
`

// _checkScript: KEYS lock, code, fail; ARGV hash, max attempts, fail max, fail window ms, lock ms.
const _checkScript = `
local lock = redis.call("PTTL", KEYS[1])
if lock > 0 then return {1, lock} end
local stored = redis.call("HGET", KEYS[2], "h")
if stored and stored == ARGV[1] then
  redis.call("DEL", KEYS[2])
  return {0, 0}
end
if stored and redis.call("HINCRBY", KEYS[2], "n", 1) >= tonumber(ARGV[2]) then redis.call("DEL", KEYS[2]) end
local f = redis.call("INCR", KEYS[3])
if f == 1 then redis.call("PEXPIRE", KEYS[3], ARGV[4]) end
if f >= tonumber(ARGV[3]) then
  redis.call("SET", KEYS[1], "1", "PX", ARGV[5])
  redis.call("DEL", KEYS[3])
end
return {3, 0}
`

// _cancelScript: KEYS code, cooldown, daily.
const _cancelScript = `
redis.call("DEL", KEYS[1], KEYS[2])
if tonumber(redis.call("GET", KEYS[3]) or "0") > 0 then redis.call("DECR", KEYS[3]) end
return 0
`

// SMSCodes keeps the verification codes under {service}:sms:, or in process without a client.
type SMSCodes struct {
	client  *goredis.Client
	prefix  string
	limits  repo.SMSLimits
	reserve *goredis.Script
	check   *goredis.Script
	cancel  *goredis.Script
	now     func() time.Time

	mu     sync.Mutex
	local  map[string]*smsEntry
	purged time.Time
}

type smsEntry struct {
	value    string
	count    int
	expires  time.Time
	attempts int
}

var _ repo.SMSCodes = (*SMSCodes)(nil)

// NewSMSCodes returns SMSCodes; client may be nil.
func NewSMSCodes(client *goredis.Client, service string, limits repo.SMSLimits) *SMSCodes {
	return &SMSCodes{
		client: client, prefix: service + ":sms:", limits: limits, reserve: goredis.NewScript(_reserveScript),
		check: goredis.NewScript(_checkScript), cancel: goredis.NewScript(_cancelScript), now: time.Now,
		mu: sync.Mutex{}, local: map[string]*smsEntry{}, purged: time.Time{},
	}
}

func (s *SMSCodes) lockKey(phone string) string     { return s.prefix + "lock:" + phone }
func (s *SMSCodes) cooldownKey(phone string) string { return s.prefix + "cooldown:" + phone }
func (s *SMSCodes) failKey(phone string) string     { return s.prefix + "fail:" + phone }

func (s *SMSCodes) dailyKey(phone, day string) string { return s.prefix + "daily:" + phone + ":" + day }

func (s *SMSCodes) codeKey(scene, phone string) string {
	return s.prefix + "code:" + scene + ":" + phone
}

func verdict(res []int64) (repo.SMSVerdict, time.Duration) {
	return repo.SMSVerdict(res[0]), time.Duration(res[1]) * time.Millisecond
}

// Reserve implements repo.SMSCodes.
func (s *SMSCodes) Reserve(ctx context.Context, phone, day string, dayTTL time.Duration) (repo.SMSVerdict, time.Duration, error) {
	if s.client == nil {
		v, wait := s.reserveLocal(phone, day, dayTTL)

		return v, wait, nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	res, err := s.reserve.Run(cmdCtx, s.client,
		[]string{s.lockKey(phone), s.cooldownKey(phone), s.dailyKey(phone, day)},
		s.limits.Cooldown.Milliseconds(), s.limits.DailyMax, dayTTL.Milliseconds()).Int64Slice()
	if err != nil {
		return repo.SMSAllowed, 0, fmt.Errorf("redis: sms reserve: %w", err)
	}

	v, wait := verdict(res)

	return v, wait, nil
}

// Save implements repo.SMSCodes.
func (s *SMSCodes) Save(ctx context.Context, scene, phone string, hash []byte) error {
	value := hex.EncodeToString(hash)

	if s.client == nil {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.local[s.codeKey(scene, phone)] = &smsEntry{value: value, count: 0, expires: s.now().Add(s.limits.CodeTTL), attempts: 0}

		return nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	key := s.codeKey(scene, phone)

	_, err := s.client.TxPipelined(cmdCtx, func(p goredis.Pipeliner) error {
		p.Del(cmdCtx, key)
		p.HSet(cmdCtx, key, "h", value, "n", 0)
		p.PExpire(cmdCtx, key, s.limits.CodeTTL)

		return nil
	})
	if err != nil {
		return fmt.Errorf("redis: sms save: %w", err)
	}

	return nil
}

// Cancel implements repo.SMSCodes.
func (s *SMSCodes) Cancel(ctx context.Context, scene, phone, day string) error {
	if s.client == nil {
		s.mu.Lock()
		defer s.mu.Unlock()

		delete(s.local, s.codeKey(scene, phone))
		delete(s.local, s.cooldownKey(phone))

		if e := s.live(s.dailyKey(phone, day)); e != nil && e.count > 0 {
			e.count--
		}

		return nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	keys := []string{s.codeKey(scene, phone), s.cooldownKey(phone), s.dailyKey(phone, day)}
	if err := s.cancel.Run(cmdCtx, s.client, keys).Err(); err != nil {
		return fmt.Errorf("redis: sms cancel: %w", err)
	}

	return nil
}

// Check implements repo.SMSCodes.
func (s *SMSCodes) Check(ctx context.Context, scene, phone string, hash []byte) (repo.SMSVerdict, time.Duration, error) {
	value := hex.EncodeToString(hash)

	if s.client == nil {
		v, wait := s.checkLocal(scene, phone, value)

		return v, wait, nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, _commandTimeout)
	defer cancel()

	res, err := s.check.Run(cmdCtx, s.client,
		[]string{s.lockKey(phone), s.codeKey(scene, phone), s.failKey(phone)},
		value, s.limits.MaxAttempts, s.limits.FailMax, s.limits.FailWindow.Milliseconds(), s.limits.LockTTL.Milliseconds()).Int64Slice()
	if err != nil {
		return repo.SMSAllowed, 0, fmt.Errorf("redis: sms check: %w", err)
	}

	v, wait := verdict(res)

	return v, wait, nil
}

// sweep drops the expired local entries at most once per _purgeInterval; the caller holds mu.
func (s *SMSCodes) sweep() {
	now := s.now()
	if now.Sub(s.purged) < _purgeInterval {
		return
	}

	s.purged = now

	for k, e := range s.local {
		if !now.Before(e.expires) {
			delete(s.local, k)
		}
	}
}

// live returns the unexpired local entry of key; the caller holds mu.
func (s *SMSCodes) live(key string) *smsEntry {
	e, ok := s.local[key]
	if !ok {
		return nil
	}

	if !s.now().Before(e.expires) {
		delete(s.local, key)

		return nil
	}

	return e
}

func (s *SMSCodes) set(key string, ttl time.Duration) *smsEntry {
	e := &smsEntry{value: "", count: 0, expires: s.now().Add(ttl), attempts: 0}
	s.local[key] = e

	return e
}

func (s *SMSCodes) reserveLocal(phone, day string, dayTTL time.Duration) (repo.SMSVerdict, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweep()

	now := s.now()

	if e := s.live(s.lockKey(phone)); e != nil {
		return repo.SMSLocked, e.expires.Sub(now)
	}

	if e := s.live(s.cooldownKey(phone)); e != nil {
		return repo.SMSTooFrequent, e.expires.Sub(now)
	}

	daily := s.live(s.dailyKey(phone, day))
	if daily != nil && daily.count >= s.limits.DailyMax {
		return repo.SMSTooFrequent, daily.expires.Sub(now)
	}

	s.set(s.cooldownKey(phone), s.limits.Cooldown)

	if daily == nil {
		daily = s.set(s.dailyKey(phone, day), dayTTL)
	}

	daily.count++

	return repo.SMSAllowed, 0
}

func (s *SMSCodes) checkLocal(scene, phone, value string) (repo.SMSVerdict, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sweep()

	if e := s.live(s.lockKey(phone)); e != nil {
		return repo.SMSLocked, e.expires.Sub(s.now())
	}

	key := s.codeKey(scene, phone)

	code := s.live(key)
	if code != nil && code.value == value {
		delete(s.local, key)

		return repo.SMSAllowed, 0
	}

	if code != nil {
		code.attempts++

		if code.attempts >= s.limits.MaxAttempts {
			delete(s.local, key)
		}
	}

	fail := s.live(s.failKey(phone))
	if fail == nil {
		fail = s.set(s.failKey(phone), s.limits.FailWindow)
	}

	fail.count++

	if fail.count >= s.limits.FailMax {
		s.set(s.lockKey(phone), s.limits.LockTTL)
		delete(s.local, s.failKey(phone))
	}

	return repo.SMSInvalid, 0
}
