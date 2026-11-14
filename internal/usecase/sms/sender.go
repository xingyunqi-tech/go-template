package sms

import (
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const _digits = 10

// Purposes of verification codes (nestjs SmsCodeType).
const (
	PurposeLogin          = "login"
	PurposeRegister       = "register"
	PurposeResetPassword  = "reset_password"
	PurposeChangePhone    = "change_phone"
	PurposeVerifyIdentity = "verify_identity"
)

// Provider delivers a template message.
type Provider interface {
	Send(ctx context.Context, phone string, params map[string]string) error
}

// Config holds the code rules.
type Config struct {
	CodeLength     int
	CodeTTL        time.Duration
	MaxAttempts    int
	ResendInterval time.Duration
}

// SendResult is the outcome of SendCode; RetryAfter is set, in seconds, when the code was sent
// too recently.
type SendResult struct {
	Sent       bool
	RetryAfter int
}

// Service issues codes through a Provider and verifies them.
type Service struct {
	*Codes

	cfg      Config
	locks    repo.Locks
	provider Provider
	log      *slog.Logger
}

// NewService returns a Service.
func NewService(cache repo.Cache, locks repo.Locks, provider Provider, cfg Config, log *slog.Logger) *Service {
	return &Service{Codes: newCodes(cache, cfg.MaxAttempts, cfg.CodeTTL), cfg: cfg, locks: locks, provider: provider, log: log}
}

func (s *Service) generate() (string, error) {
	var b strings.Builder

	for range s.cfg.CodeLength {
		n, err := rand.Int(rand.Reader, big.NewInt(_digits))
		if err != nil {
			return "", fmt.Errorf("sms: %w", err)
		}

		b.WriteString(n.String())
	}

	return b.String(), nil
}

func sendLockKey(phone, purpose string) string {
	return "sms:send:" + purpose + ":" + phone
}

// SendCode sends a new code unless one was sent within the resend interval.
func (s *Service) SendCode(ctx context.Context, phone, purpose string) (SendResult, error) {
	key := CacheKey(phone, purpose)
	interval := s.cfg.ResendInterval

	var existing stored

	found, err := s.cache.Get(ctx, key, &existing)
	if err != nil {
		return SendResult{}, fmt.Errorf("sms: %w", err)
	}

	if found {
		if elapsed := s.now().Sub(time.UnixMilli(existing.CreatedAt)); elapsed < interval {
			return SendResult{Sent: false, RetryAfter: int(math.Ceil((interval - elapsed).Seconds()))}, nil
		}
	}

	token, ok := s.locks.TryAcquire(ctx, sendLockKey(phone, purpose), interval)
	if !ok {
		return SendResult{Sent: false, RetryAfter: int(interval.Seconds())}, nil
	}

	code, err := s.generate()
	if err != nil {
		s.locks.Release(ctx, sendLockKey(phone, purpose), token)

		return SendResult{}, err
	}

	if err := s.provider.Send(ctx, phone, map[string]string{"code": code}); err != nil {
		s.locks.Release(ctx, sendLockKey(phone, purpose), token)
		s.log.ErrorContext(ctx, "Failed to send SMS", "phone", entity.MaskPhone(phone), "error", err.Error())

		return SendResult{Sent: false, RetryAfter: 0}, nil
	}

	if err := s.cache.Set(ctx, key, stored{Code: code, Attempts: 0, CreatedAt: s.now().UnixMilli()}, s.cfg.CodeTTL); err != nil {
		s.locks.Release(ctx, sendLockKey(phone, purpose), token)

		return SendResult{}, fmt.Errorf("sms: %w", err)
	}

	s.log.InfoContext(ctx, "Verification code sent", "phone", entity.MaskPhone(phone), "type", purpose)

	return SendResult{Sent: true, RetryAfter: 0}, nil
}
