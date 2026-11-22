package usecase

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

// Limiter checks rate limit rules.
type Limiter interface {
	Consume(ctx context.Context, rule ratelimit.Rule, subject string) (ratelimit.Decision, error)
	Peek(ctx context.Context, rule ratelimit.Rule, subject string) (ratelimit.Decision, error)
	Reset(ctx context.Context, rule ratelimit.Rule, subject string) error
}

// Limits counts the shared rules whose subject only a use case knows; a Redis failure is 503.
type Limits struct {
	Limiter Limiter
	Enabled bool
}

func (l Limits) check(ctx context.Context, name, subject string, peek bool) error {
	if !l.Enabled || l.Limiter == nil {
		return nil
	}

	rule := ratelimit.Shared()[name].Rule

	check := l.Limiter.Consume
	if peek {
		check = l.Limiter.Peek
	}

	d, err := check(ctx, rule, subject)
	if err != nil {
		return entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	if !d.Allowed {
		return entity.New(entity.KeyTooManyRequests).WithRetryAfter(d.RetryAfterSeconds())
	}

	return nil
}

// Consume counts a request under the shared rule name.
func (l Limits) Consume(ctx context.Context, name, subject string) error {
	return l.check(ctx, name, subject, false)
}

// Peek rejects a request the shared rule name would reject, without counting it.
func (l Limits) Peek(ctx context.Context, name, subject string) error {
	return l.check(ctx, name, subject, true)
}

// Reset forgets the failures counted under the shared rule name.
func (l Limits) Reset(ctx context.Context, name, subject string) error {
	if !l.Enabled || l.Limiter == nil {
		return nil
	}

	if err := l.Limiter.Reset(ctx, ratelimit.Shared()[name].Rule, subject); err != nil {
		return entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	return nil
}
