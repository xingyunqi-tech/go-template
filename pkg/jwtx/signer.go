package jwtx

import (
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

// Signer issues access tokens with one Ed25519 key.
type Signer struct {
	key      jwk.Key
	issuer   string
	audience string
}

// NewSigner returns a Signer whose tokens carry the kid of priv.
func NewSigner(priv ed25519.PrivateKey, issuer, audience string) (*Signer, error) {
	key, err := jwk.Import(priv)
	if err != nil {
		return nil, fmt.Errorf("jwtx: %w", err)
	}

	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errNotEd25519
	}

	kid, err := KeyID(pub)
	if err != nil {
		return nil, err
	}

	if err := key.Set(jwk.KeyIDKey, kid); err != nil {
		return nil, fmt.Errorf("jwtx: %w", err)
	}

	return &Signer{key: key, issuer: issuer, audience: audience}, nil
}

// Sign returns the compact JWS of c; c.IssuedAt and c.Expires are taken as given.
func (s *Signer) Sign(c *Claims) (string, error) {
	tok, err := jwt.NewBuilder().
		Issuer(s.issuer).
		Audience([]string{s.audience}).
		Subject(c.Subject.String()).
		JwtID(c.ID).
		IssuedAt(c.IssuedAt).
		Expiration(c.Expires).
		Claim("sid", c.SessionID.String()).
		Claim("roles", c.Roles).
		Claim("email_verified", c.EmailVerified).
		Claim("auth_time", c.AuthTime.Unix()).
		Build()
	if err != nil {
		return "", fmt.Errorf("jwtx: %w", err)
	}

	tok.Options().Enable(jwt.FlattenAudience)

	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.EdDSA(), s.key, jws.WithProtectedHeaders(typHeader())))
	if err != nil {
		return "", fmt.Errorf("jwtx: sign: %w", err)
	}

	return string(signed), nil
}

func typHeader() jws.Headers {
	h := jws.NewHeaders()
	_ = h.Set(jws.TypeKey, "JWT")

	return h
}

// Lifetime returns the iat and exp of a token issued at now and valid for ttl, in whole seconds.
func Lifetime(now time.Time, ttl time.Duration) (iat, exp time.Time) {
	iat = now.Truncate(time.Second)

	return iat, iat.Add(ttl)
}
