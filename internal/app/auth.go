package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

var errNotEd25519 = errors.New("app: signing key is not Ed25519")

// Keys are the token keys of the authentication role: Signer is nil for a verifier.
type Keys struct {
	Signer   *jwtx.Signer
	Static   *jwtx.StaticKeys
	Verifier *jwtx.Verifier
}

// NewKeys loads AUTH_SIGNING_KEY and AUTH_VERIFY_KEYS for the issuer, registers AUTH_JWKS_URL for
// a verifier, or generates a temporary key outside production.
func NewKeys(ctx context.Context, cfg *config.Auth, log *slog.Logger) (*Keys, error) {
	if cfg.Role() == config.RoleVerifier {
		remote, err := jwtx.NewRemoteKeys(ctx, cfg.JWKSURL)
		if err != nil {
			return nil, fmt.Errorf("app: AUTH_JWKS_URL: %w", err)
		}

		return &Keys{Signer: nil, Static: nil, Verifier: jwtx.NewVerifier(remote, cfg.Issuer, cfg.Audience)}, nil
	}

	priv, err := signingKey(cfg, log)
	if err != nil {
		return nil, err
	}

	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errNotEd25519
	}

	pubs := []ed25519.PublicKey{pub}

	if cfg.VerifyKeys != "" {
		old, err := jwtx.ParsePublicKeys(cfg.VerifyKeys)
		if err != nil {
			return nil, fmt.Errorf("app: AUTH_VERIFY_KEYS: %w", err)
		}

		pubs = append(pubs, old...)
	}

	static, err := jwtx.NewStaticKeys(pubs...)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	signer, err := jwtx.NewSigner(priv, cfg.Issuer, cfg.Audience)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}

	return &Keys{Signer: signer, Static: static, Verifier: jwtx.NewVerifier(static, cfg.Issuer, cfg.Audience)}, nil
}

func signingKey(cfg *config.Auth, log *slog.Logger) (ed25519.PrivateKey, error) {
	if cfg.Role() == config.RoleIssuer {
		priv, err := jwtx.ParsePrivateKey(cfg.SigningKey.Value())
		if err != nil {
			return nil, fmt.Errorf("app: AUTH_SIGNING_KEY: %w", err)
		}

		return priv, nil
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("app: generate signing key: %w", err)
	}

	log.Warn("AUTH_SIGNING_KEY and AUTH_JWKS_URL are unset: signing with a temporary key, tokens stop working on restart")

	return priv, nil
}
