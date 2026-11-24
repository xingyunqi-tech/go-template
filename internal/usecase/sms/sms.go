// Package sms sends and checks the SMS verification codes (platform conventions §11.1).
package sms

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
)

const (
	_codeDigits    = 6
	_codeTTL       = 5 * time.Minute
	_dailyMax      = 10
	_maxAttempts   = 5
	_failMax       = 10
	_beijingOffset = 8 * 60 * 60
	_dayLayout     = "20060102"
)

// Limits are the code rules of platform conventions §11.1.
func Limits() repo.SMSLimits {
	return repo.SMSLimits{
		CodeTTL: _codeTTL, Cooldown: time.Minute, DailyMax: _dailyMax, MaxAttempts: _maxAttempts,
		FailWindow: time.Hour, FailMax: _failMax, LockTTL: time.Hour,
	}
}

// Service implements usecase.SMS and checks the codes for the sign-in and binding use cases.
type Service struct {
	codes  repo.SMSCodes
	sender repo.SMSSender
	now    func() time.Time
}

var _ usecase.SMS = (*Service)(nil)

// New returns a Service; now nil is time.Now.
func New(codes repo.SMSCodes, sender repo.SMSSender, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}

	return &Service{codes: codes, sender: sender, now: now}
}

// day is the Beijing date the daily limit counts and the time left of it.
func (s *Service) day() (string, time.Duration) {
	now := s.now().In(time.FixedZone("CST", _beijingOffset))
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())

	return now.Format(_dayLayout), next.Sub(now)
}

func hash(scene, phone, code string) []byte {
	sum := sha256.Sum256([]byte(scene + ":" + phone + ":" + code))

	return sum[:]
}

func newCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(math.Pow10(_codeDigits))))
	if err != nil {
		return "", fmt.Errorf("sms: random code: %w", err)
	}

	return fmt.Sprintf("%0*d", _codeDigits, n.Int64()), nil
}

func seconds(d time.Duration) int64 {
	return int64(math.Ceil(d.Seconds()))
}

func rejected(v repo.SMSVerdict, wait time.Duration) error {
	switch v {
	case repo.SMSLocked:
		return entity.New(entity.KeyVerificationLocked).WithRetryAfter(seconds(wait))
	case repo.SMSTooFrequent:
		return entity.New(entity.KeySendTooFrequent).WithRetryAfter(seconds(wait))
	case repo.SMSInvalid:
		return entity.New(entity.KeyVerificationCodeInvalid)
	case repo.SMSAllowed:
	}

	return nil
}

// Send implements usecase.SMS: bind_phone needs a signed-in caller. The gateway call outlives a
// canceled request; a failed delivery is common.badGateway and takes the send back only when the
// message surely did not go out.
func (s *Service) Send(ctx context.Context, caller *usecase.Caller, phone, scene string) error {
	if scene == entity.SMSSceneBindPhone && caller == nil {
		return entity.New(entity.KeyUnauthorized)
	}

	e164, err := usecase.NormalizePhone("phone", phone)
	if err != nil {
		return err //nolint:wrapcheck // entity errors
	}

	day, left := s.day()

	v, wait, err := s.codes.Reserve(ctx, e164, day, left)
	if err != nil {
		return entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	if err := rejected(v, wait); err != nil {
		return err
	}

	code, err := newCode()
	if err != nil {
		return err
	}

	return s.deliver(context.WithoutCancel(ctx), scene, e164, day, code)
}

func (s *Service) deliver(ctx context.Context, scene, e164, day, code string) error {
	if err := s.codes.Save(ctx, scene, e164, hash(scene, e164, code)); err != nil {
		return errors.Join(entity.Wrap(entity.KeyServiceUnavailable, err), s.cancel(ctx, scene, e164, day))
	}

	err := s.sender.Send(ctx, e164, code)
	if err != nil && errors.Is(err, repo.ErrSMSNotSent) {
		if cancelErr := s.cancel(ctx, scene, e164, day); cancelErr != nil {
			return errors.Join(err, cancelErr)
		}
	}

	return err //nolint:wrapcheck // entity errors of the sender
}

func (s *Service) cancel(ctx context.Context, scene, phone, day string) error {
	if err := s.codes.Cancel(ctx, scene, phone, day); err != nil {
		return entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	return nil
}

// Verify consumes the code of (scene, phone); phone is E.164. A wrong code is
// auth.verificationCodeInvalid, a locked phone auth.verificationLocked.
func (s *Service) Verify(ctx context.Context, scene, phone, code string) error {
	v, wait, err := s.codes.Check(ctx, scene, phone, hash(scene, phone, code))
	if err != nil {
		return entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	return rejected(v, wait)
}
