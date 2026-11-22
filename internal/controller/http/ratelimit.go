package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/clientip"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

var errUnknownRule = errors.New("unknown or use-case rate limit rule")

// checkRateLimits reports policy rules the router cannot count: unknown names and rules whose
// subject only the use case knows.
func checkRateLimits(id string, policy *Policy) error {
	shared := ratelimit.Shared()

	var errs []error

	for _, name := range policy.RateLimits {
		rule, ok := shared[name]
		if !ok || (rule.Subject != ratelimit.SubjectIP && rule.Subject != ratelimit.SubjectUser && rule.Subject != ratelimit.SubjectUserOrIP) {
			errs = append(errs, fmt.Errorf("%w: %s uses %s", errUnknownRule, id, name))
		}

		if ok && rule.Subject == ratelimit.SubjectUser && policy.Auth != AuthJWT {
			errs = append(errs, fmt.Errorf("%w: %s counts %s per user but is not JWT", errUnknownRule, id, name))
		}
	}

	return errors.Join(errs...)
}

// limitHeaders collects the decisions of one request for the X-RateLimit-* headers.
type limitHeaders struct {
	w         http.ResponseWriter
	decisions []ratelimit.Decision
	rejected  bool
}

func newLimitHeaders(w http.ResponseWriter) *limitHeaders {
	return &limitHeaders{w: w, decisions: nil, rejected: false}
}

func (l *limitHeaders) add(d ratelimit.Decision) {
	l.decisions = append(l.decisions, d)
	l.rejected = l.rejected || !d.Allowed
}

// write sets the headers of the tightest rule; a rejected request only gets Retry-After.
func (l *limitHeaders) write() {
	if l.rejected {
		return
	}

	d, ok := ratelimit.Tightest(l.decisions...)
	if !ok {
		return
	}

	h := l.w.Header()
	h.Set("X-RateLimit-Limit", strconv.Itoa(d.Limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(d.Remaining))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(d.ResetSeconds(), 10))
}

// tooManyRequests is the 429 of a rejected decision.
func tooManyRequests(w http.ResponseWriter, d ratelimit.Decision) error {
	w.Header().Set("Retry-After", strconv.FormatInt(d.RetryAfterSeconds(), 10))

	return entity.New(entity.KeyTooManyRequests)
}

// limitIP counts the {service}-ip rule; a Redis failure lets the request pass.
func (p *pipeline) limitIP(r *http.Request, limits *limitHeaders) error {
	if !p.opts.RateLimits {
		return nil
	}

	subject := ratelimit.IPSubject(clientip.Subject(p.opts.TrustedProxies.ClientIP(r)))

	return p.consume(r, ratelimit.PrivateIP(p.opts.ServiceName), subject, false, limits)
}

// limitPolicy counts the {service}-user rule for signed-in requests and the shared rules of the
// policy; a Redis failure rejects with 503 for shared rules only.
func (p *pipeline) limitPolicy(r *http.Request, policy *Policy, limits *limitHeaders) error {
	if !p.opts.RateLimits {
		return nil
	}

	principal := reqctx.PrincipalOf(r.Context())
	ipSubject := ratelimit.IPSubject(clientip.Subject(p.opts.TrustedProxies.ClientIP(r)))

	if principal != nil {
		subject := ratelimit.UserSubject(principal.Subject.String())
		if err := p.consume(r, ratelimit.PrivateUser(p.opts.ServiceName), subject, false, limits); err != nil {
			return err
		}
	}

	shared := ratelimit.Shared()

	for _, name := range policy.RateLimits {
		rule := shared[name]

		subject := ipSubject
		if principal != nil && rule.Subject != ratelimit.SubjectIP {
			subject = ratelimit.UserSubject(principal.Subject.String())
		}

		if err := p.consume(r, rule.Rule, subject, true, limits); err != nil {
			return err
		}
	}

	return nil
}

func (p *pipeline) consume(r *http.Request, rule ratelimit.Rule, subject string, shared bool, limits *limitHeaders) error {
	d, err := p.opts.Limiter.Consume(r.Context(), rule, subject)

	switch {
	case err != nil && shared:
		return entity.Wrap(entity.KeyServiceUnavailable, err)
	case err != nil:
		p.opts.Log.WarnContext(r.Context(), "rate limit skipped", "rule", rule.Name, "error", err.Error())

		return nil
	}

	limits.add(d)

	if !d.Allowed {
		return tooManyRequests(limits.w, d)
	}

	return nil
}
