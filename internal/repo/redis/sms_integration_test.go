//go:build integration

package redis_test

import (
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
)

const _smsPhone = "+8613800138000"

func smsLimits() repo.SMSLimits {
	return repo.SMSLimits{
		CodeTTL: 5 * time.Minute, Cooldown: time.Minute, DailyMax: 3, MaxAttempts: 2,
		FailWindow: time.Hour, FailMax: 3, LockTTL: time.Hour,
	}
}

func TestSMSReserveCoolsDownAndCountsTheDay(t *testing.T) {
	t.Parallel()

	client := newClient(t)
	codes := redis.NewSMSCodes(client, "svc", smsLimits())

	v, _, err := codes.Reserve(t.Context(), _smsPhone, "20260101", 10*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, repo.SMSAllowed, v)

	v, wait, err := codes.Reserve(t.Context(), _smsPhone, "20260101", 10*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, repo.SMSTooFrequent, v)
	assert.InDelta(t, 60, wait.Seconds(), 2)

	ttl, err := client.PTTL(t.Context(), "svc:sms:daily:"+_smsPhone+":20260101").Result()
	require.NoError(t, err)
	assert.InDelta(t, (10 * time.Hour).Seconds(), ttl.Seconds(), 5)

	for range 2 {
		require.NoError(t, client.Del(t.Context(), "svc:sms:cooldown:"+_smsPhone).Err())

		v, _, err = codes.Reserve(t.Context(), _smsPhone, "20260101", 10*time.Hour)
		require.NoError(t, err)
		assert.Equal(t, repo.SMSAllowed, v)
	}

	require.NoError(t, client.Del(t.Context(), "svc:sms:cooldown:"+_smsPhone).Err())

	v, wait, err = codes.Reserve(t.Context(), _smsPhone, "20260101", 10*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, repo.SMSTooFrequent, v)
	assert.InDelta(t, (10 * time.Hour).Seconds(), wait.Seconds(), 5)
}

func TestSMSCancelTakesTheSendBack(t *testing.T) {
	t.Parallel()

	client := newClient(t)
	codes := redis.NewSMSCodes(client, "svc", smsLimits())

	_, _, err := codes.Reserve(t.Context(), _smsPhone, "20260101", time.Hour)
	require.NoError(t, err)
	require.NoError(t, codes.Save(t.Context(), "login", _smsPhone, []byte("h")))
	require.NoError(t, codes.Cancel(t.Context(), "login", _smsPhone, "20260101"))

	n, err := client.Get(t.Context(), "svc:sms:daily:"+_smsPhone+":20260101").Int()
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	v, _, err := codes.Check(t.Context(), "login", _smsPhone, []byte("h"))
	require.NoError(t, err)
	assert.Equal(t, repo.SMSInvalid, v)

	v, _, err = codes.Reserve(t.Context(), _smsPhone, "20260101", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, repo.SMSAllowed, v)
}

func TestSMSCheckConsumesTheCodeAndLocksAfterFailures(t *testing.T) {
	t.Parallel()

	client := newClient(t)
	codes := redis.NewSMSCodes(client, "svc", smsLimits())

	require.NoError(t, codes.Save(t.Context(), "login", _smsPhone, []byte("h")))

	ttl, err := client.PTTL(t.Context(), "svc:sms:code:login:"+_smsPhone).Result()
	require.NoError(t, err)
	assert.InDelta(t, 300, ttl.Seconds(), 2)

	v, _, err := codes.Check(t.Context(), "bind_phone", _smsPhone, []byte("h"))
	require.NoError(t, err)
	assert.Equal(t, repo.SMSInvalid, v)

	v, _, err = codes.Check(t.Context(), "login", _smsPhone, []byte("h"))
	require.NoError(t, err)
	assert.Equal(t, repo.SMSAllowed, v)

	require.NoError(t, codes.Save(t.Context(), "login", _smsPhone, []byte("h")))

	for range 2 {
		v, _, err = codes.Check(t.Context(), "login", _smsPhone, []byte("x"))
		require.NoError(t, err)
		assert.Equal(t, repo.SMSInvalid, v)
	}

	v, wait, err := codes.Check(t.Context(), "login", _smsPhone, []byte("h"))
	require.NoError(t, err)
	assert.Equal(t, repo.SMSLocked, v)
	assert.InDelta(t, 3600, wait.Seconds(), 5)

	v, _, err = codes.Reserve(t.Context(), _smsPhone, "20260101", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, repo.SMSLocked, v)
}

func TestSMSCodesReportRedisFailures(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1}) //nolint:exhaustruct // defaults

	t.Cleanup(func() { _ = client.Close() })

	codes := redis.NewSMSCodes(client, "svc", smsLimits())

	_, _, err := codes.Reserve(t.Context(), _smsPhone, "20260101", time.Hour)
	require.Error(t, err)
	require.Error(t, codes.Save(t.Context(), "login", _smsPhone, []byte("h")))
	require.Error(t, codes.Cancel(t.Context(), "login", _smsPhone, "20260101"))

	_, _, err = codes.Check(t.Context(), "login", _smsPhone, []byte("h"))
	require.Error(t, err)
}
