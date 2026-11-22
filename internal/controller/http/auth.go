package http

import (
	"errors"
	"net/http"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

// _reauthWindow is how recent auth_time must be for policies with Reauth.
const _reauthWindow = 10 * time.Minute

var errRevoked = errors.New("session revoked")

// authenticate verifies the access token of JWT and optional operations; public operations
// never read Authorization.
func (p *pipeline) authenticate(r *http.Request, policy *Policy) (*http.Request, error) {
	if policy.Auth == AuthPublic {
		return r, nil
	}

	token, present := bearer(r)

	principal, err := p.principal(r, token, present)

	switch {
	case err == nil:
		setUserID(r.Context(), principal.Subject.String())

		return r.WithContext(reqctx.WithPrincipal(r.Context(), principal)), nil
	case isServiceUnavailable(err):
		return r, err
	case policy.Auth == AuthOptional:
		return r, nil
	default:
		return r, err
	}
}

func (p *pipeline) principal(r *http.Request, token string, present bool) (*reqctx.Principal, error) {
	if !present {
		return nil, entity.New(entity.KeyUnauthorized)
	}

	if token == "" {
		return nil, entity.Wrap(entity.KeyUnauthorized, httperr.ErrInvalidToken)
	}

	claims, err := p.opts.Verifier.Verify(r.Context(), token)

	switch {
	case errors.Is(err, jwtx.ErrExpired):
		return nil, entity.Wrap(entity.KeyTokenExpired, httperr.ErrInvalidToken)
	case errors.Is(err, jwtx.ErrKeysUnavailable):
		return nil, entity.Wrap(entity.KeyServiceUnavailable, err)
	case err != nil:
		return nil, entity.Wrap(entity.KeyUnauthorized, errors.Join(httperr.ErrInvalidToken, err))
	}

	revoked, err := p.opts.Revocations.IsRevoked(r.Context(), claims.SessionID)

	switch {
	case err != nil:
		return nil, entity.Wrap(entity.KeyServiceUnavailable, err)
	case revoked:
		return nil, entity.Wrap(entity.KeyUnauthorized, errors.Join(httperr.ErrInvalidToken, errRevoked))
	}

	return &reqctx.Principal{
		Subject: claims.Subject, SessionID: claims.SessionID, Roles: claims.Roles,
		EmailVerified: claims.EmailVerified, AuthTime: claims.AuthTime,
	}, nil
}

// authorize checks permissions, recent sign-in and the verified email the policy asks for.
func (p *pipeline) authorize(r *http.Request, policy *Policy) error {
	principal := reqctx.PrincipalOf(r.Context())

	if !policy.Perms.empty() && (principal == nil || !policy.Perms.granted(p.opts.Authorizer, principal.Roles)) {
		return entity.New(entity.KeyForbidden)
	}

	if policy.Reauth && (principal == nil || p.opts.Now().Sub(principal.AuthTime) > _reauthWindow) {
		return entity.New(entity.KeyReauthRequired)
	}

	if policy.VerifiedEmail && (principal == nil || !principal.EmailVerified) {
		return entity.New(entity.KeyEmailNotVerified)
	}

	return nil
}
