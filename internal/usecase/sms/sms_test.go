package sms_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
)

const _phone = "+8613800138000"

type sender struct {
	codes       map[string]string
	err         error
	requireLive bool
}

func (s *sender) Send(ctx context.Context, phone, code string) error {
	if s.err != nil {
		return s.err
	}

	if s.requireLive && ctx.Err() != nil {
		return ctx.Err()
	}

	s.codes[phone] = code

	return nil
}

func newService() (*sms.Service, *sender) {
	out := &sender{codes: map[string]string{}, err: nil, requireLive: false}

	return sms.New(redis.NewSMSCodes(nil, "svc", sms.Limits()), out, nil), out
}

func caller() *usecase.Caller {
	return &usecase.Caller{UserID: uuid.NewV7(), SessionID: uuid.NewV7(), AuthTime: time.Now()}
}

func wrong(code string) string {
	if code == "000000" {
		return "111111"
	}

	return "000000"
}

func TestSendDeliversASixDigitCodeThatVerifiesOnce(t *testing.T) {
	t.Parallel()

	svc, out := newService()

	require.NoError(t, svc.Send(t.Context(), nil, "13800138000", entity.SMSSceneLogin))
	require.Regexp(t, `^\d{6}$`, out.codes[_phone])

	require.ErrorIs(t, svc.Verify(t.Context(), entity.SMSSceneBindPhone, _phone, out.codes[_phone]), entity.New(entity.KeyVerificationCodeInvalid))
	require.NoError(t, svc.Verify(t.Context(), entity.SMSSceneLogin, _phone, out.codes[_phone]))
	require.ErrorIs(t, svc.Verify(t.Context(), entity.SMSSceneLogin, _phone, out.codes[_phone]), entity.New(entity.KeyVerificationCodeInvalid))
}

func TestBindPhoneCodesNeedACaller(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	require.ErrorIs(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneBindPhone), entity.New(entity.KeyUnauthorized))
	require.NoError(t, svc.Send(t.Context(), caller(), _phone, entity.SMSSceneBindPhone))
}

func TestSendRejectsAnInvalidPhone(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	var verr *entity.ValidationError

	require.ErrorAs(t, svc.Send(t.Context(), nil, "123", entity.SMSSceneLogin), &verr)
	assert.Equal(t, "phone", verr.Errors[0].Field)
}

func TestSendCoolsDownForAMinute(t *testing.T) {
	t.Parallel()

	svc, _ := newService()

	require.NoError(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin))

	err := svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin)
	require.ErrorIs(t, err, entity.New(entity.KeySendTooFrequent))

	var e *entity.Error

	require.ErrorAs(t, err, &e)
	assert.InDelta(t, 60, e.RetryAfter, 1)
}

func TestRefusedDeliveryIsBadGatewayAndCanBeRetried(t *testing.T) {
	t.Parallel()

	svc, out := newService()
	out.err = entity.Wrap(entity.KeyBadGateway, fmt.Errorf("%w: limit", repo.ErrSMSNotSent))

	require.ErrorIs(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin), entity.New(entity.KeyBadGateway))

	out.err = nil

	require.NoError(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin))
}

func TestUncertainDeliveryKeepsTheCooldown(t *testing.T) {
	t.Parallel()

	svc, out := newService()
	out.err = entity.Wrap(entity.KeyBadGateway, errors.New("timeout"))

	require.ErrorIs(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin), entity.New(entity.KeyBadGateway))

	out.err = nil

	require.ErrorIs(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin), entity.New(entity.KeySendTooFrequent))
}

func TestDeliveryOutlivesACanceledRequest(t *testing.T) {
	t.Parallel()

	svc, out := newService()
	out.requireLive = true

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, svc.Send(ctx, nil, _phone, entity.SMSSceneLogin))
	assert.NotEmpty(t, out.codes[_phone])
}

func TestFailedSaveTakesTheSendBack(t *testing.T) {
	t.Parallel()

	codes := mocks.NewMockSMSCodes(gomock.NewController(t))
	svc := sms.New(codes, &sender{codes: map[string]string{}}, func() time.Time { return time.Date(2026, 1, 1, 16, 30, 0, 0, time.UTC) })

	codes.EXPECT().Reserve(gomock.Any(), _phone, "20260102", 23*time.Hour+30*time.Minute).Return(repo.SMSAllowed, time.Duration(0), nil)
	codes.EXPECT().Save(gomock.Any(), entity.SMSSceneLogin, _phone, gomock.Any()).Return(errors.New("redis down"))
	codes.EXPECT().Cancel(gomock.Any(), entity.SMSSceneLogin, _phone, "20260102").Return(nil)

	require.ErrorIs(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin), entity.New(entity.KeyServiceUnavailable))
}

func TestACodeAllowsFiveAttempts(t *testing.T) {
	t.Parallel()

	svc, out := newService()

	require.NoError(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin))

	code := out.codes[_phone]
	for range 5 {
		require.ErrorIs(t, svc.Verify(t.Context(), entity.SMSSceneLogin, _phone, wrong(code)), entity.New(entity.KeyVerificationCodeInvalid))
	}

	require.ErrorIs(t, svc.Verify(t.Context(), entity.SMSSceneLogin, _phone, code), entity.New(entity.KeyVerificationCodeInvalid))
}

func TestTenFailuresLockThePhoneForAnHour(t *testing.T) {
	t.Parallel()

	svc, out := newService()

	require.NoError(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin))

	code := out.codes[_phone]
	for range 10 {
		require.ErrorIs(t, svc.Verify(t.Context(), entity.SMSSceneLogin, _phone, wrong(code)), entity.New(entity.KeyVerificationCodeInvalid))
	}

	err := svc.Verify(t.Context(), entity.SMSSceneLogin, _phone, code)
	require.ErrorIs(t, err, entity.New(entity.KeyVerificationLocked))

	var e *entity.Error

	require.ErrorAs(t, err, &e)
	assert.InDelta(t, 3600, e.RetryAfter, 1)

	require.ErrorIs(t, svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin), entity.New(entity.KeyVerificationLocked))
}

func TestRefusedDeliveryKeepsTheGatewayErrorWhenTheCancelFails(t *testing.T) {
	t.Parallel()

	codes := mocks.NewMockSMSCodes(gomock.NewController(t))
	refused := entity.Wrap(entity.KeyBadGateway, fmt.Errorf("%w: limit", repo.ErrSMSNotSent))
	redisDown := errors.New("redis down")
	svc := sms.New(codes, &sender{codes: map[string]string{}, err: refused}, nil)

	codes.EXPECT().Reserve(gomock.Any(), _phone, gomock.Any(), gomock.Any()).Return(repo.SMSAllowed, time.Duration(0), nil)
	codes.EXPECT().Save(gomock.Any(), entity.SMSSceneLogin, _phone, gomock.Any()).Return(nil)
	codes.EXPECT().Cancel(gomock.Any(), entity.SMSSceneLogin, _phone, gomock.Any()).Return(redisDown)

	err := svc.Send(t.Context(), nil, _phone, entity.SMSSceneLogin)

	var e *entity.Error

	require.ErrorAs(t, err, &e)
	assert.Equal(t, entity.KeyBadGateway, e.Key)
	require.ErrorIs(t, err, redisDown)
}
