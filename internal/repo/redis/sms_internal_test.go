package redis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

func TestLocalSMSEntriesExpireOutOfMemory(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	codes := NewSMSCodes(nil, "svc", repo.SMSLimits{
		CodeTTL: time.Minute, Cooldown: time.Minute, DailyMax: 10, MaxAttempts: 5, FailWindow: time.Hour, FailMax: 10, LockTTL: time.Hour,
	})
	codes.now = func() time.Time { return now }

	v, _, _ := codes.Reserve(t.Context(), "+8613800138000", "20260101", time.Hour)
	assert.Equal(t, repo.SMSAllowed, v)
	assert.Len(t, codes.local, 2)

	now = now.Add(2 * time.Hour)

	v, _, _ = codes.Reserve(t.Context(), "+8613800138001", "20260101", time.Hour)
	assert.Equal(t, repo.SMSAllowed, v)
	assert.Len(t, codes.local, 2)
}

func TestLocalSMSChecksAlsoExpireEntries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	codes := NewSMSCodes(nil, "svc", repo.SMSLimits{
		CodeTTL: time.Minute, Cooldown: time.Minute, DailyMax: 10, MaxAttempts: 5, FailWindow: time.Hour, FailMax: 10, LockTTL: time.Hour,
	})
	codes.now = func() time.Time { return now }

	v, _, _ := codes.Check(t.Context(), "login", "+8613800138000", []byte("x"))
	assert.Equal(t, repo.SMSInvalid, v)
	assert.Len(t, codes.local, 1)

	now = now.Add(2 * time.Hour)

	v, _, _ = codes.Check(t.Context(), "login", "+8613800138001", []byte("x"))
	assert.Equal(t, repo.SMSInvalid, v)
	assert.Len(t, codes.local, 1)
}
