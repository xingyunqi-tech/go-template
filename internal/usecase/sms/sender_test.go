package sms_test

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
)

var errDelivery = errors.New("delivery failed")

type provider struct {
	sent []string
	err  error
}

func (p *provider) Send(_ context.Context, _ string, params map[string]string) error {
	if p.err != nil {
		return p.err
	}

	p.sent = append(p.sent, params["code"])

	return nil
}

func config() sms.Config {
	return sms.Config{CodeLength: 4, CodeTTL: 5 * time.Minute, MaxAttempts: 2, ResendInterval: time.Minute}
}

func newService(p *provider) (*sms.Service, *redis.Memory, *redis.Locks) {
	cache := redis.NewMemory()
	locks := redis.NewLocks(nil, slog.New(slog.DiscardHandler))

	return sms.NewService(cache, locks, p, config(), slog.New(slog.DiscardHandler)), cache, locks
}

func TestSendCodeStoresAndVerifiesCode(t *testing.T) {
	t.Parallel()

	p := &provider{sent: nil, err: nil}
	svc, cache, _ := newService(p)

	res, err := svc.SendCode(t.Context(), "13800138000", sms.PurposeLogin)
	require.NoError(t, err)
	assert.Equal(t, sms.SendResult{Sent: true, RetryAfter: 0}, res)
	require.Len(t, p.sent, 1)
	assert.Regexp(t, regexp.MustCompile(`^\d{4}$`), p.sent[0])

	var s stored

	found, err := cache.Get(t.Context(), sms.CacheKey("13800138000", sms.PurposeLogin), &s)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, p.sent[0], s.Code)
	assert.Equal(t, 0, s.Attempts)

	again, err := svc.SendCode(t.Context(), "13800138000", sms.PurposeLogin)
	require.NoError(t, err)
	assert.False(t, again.Sent)
	assert.InDelta(t, 60, again.RetryAfter, 1)

	ok, err := svc.Verify(t.Context(), "13800138000", p.sent[0], sms.PurposeLogin)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestSendCodeHonorsSendLock(t *testing.T) {
	t.Parallel()

	svc, _, locks := newService(&provider{sent: nil, err: nil})

	_, ok := locks.TryAcquire(t.Context(), "sms:send:register:13800138000", time.Minute)
	require.True(t, ok)

	res, err := svc.SendCode(t.Context(), "13800138000", sms.PurposeRegister)
	require.NoError(t, err)
	assert.Equal(t, sms.SendResult{Sent: false, RetryAfter: 60}, res)
}

func TestSendCodeReleasesLockWhenDeliveryFails(t *testing.T) {
	t.Parallel()

	p := &provider{sent: nil, err: errDelivery}
	svc, cache, _ := newService(p)

	res, err := svc.SendCode(t.Context(), "13800138000", sms.PurposeLogin)
	require.NoError(t, err)
	assert.Equal(t, sms.SendResult{Sent: false, RetryAfter: 0}, res)

	found, err := cache.Get(t.Context(), sms.CacheKey("13800138000", sms.PurposeLogin), &stored{})
	require.NoError(t, err)
	assert.False(t, found)

	p.err = nil
	res, err = svc.SendCode(t.Context(), "13800138000", sms.PurposeLogin)
	require.NoError(t, err)
	assert.True(t, res.Sent)
}

func TestVerifyUsesConfiguredAttempts(t *testing.T) {
	t.Parallel()

	p := &provider{sent: nil, err: nil}
	svc, _, _ := newService(p)

	_, err := svc.SendCode(t.Context(), "13800138000", sms.PurposeLogin)
	require.NoError(t, err)

	for range 2 {
		ok, err := svc.Verify(t.Context(), "13800138000", "wrong", sms.PurposeLogin)
		require.NoError(t, err)
		assert.False(t, ok)
	}

	ok, err := svc.Verify(t.Context(), "13800138000", p.sent[0], sms.PurposeLogin)
	require.NoError(t, err)
	assert.False(t, ok)
}
