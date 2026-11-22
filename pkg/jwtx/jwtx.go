package jwtx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
)

const (
	_clockSkew = 30 * time.Second
	_jwsParts  = 3
)

var (
	// ErrInvalid reports a malformed token, a bad signature, a wrong issuer or audience, a
	// missing claim, or a key that is unknown or not Ed25519.
	ErrInvalid = errors.New("jwtx: invalid token")
	// ErrExpired reports a token past exp.
	ErrExpired = errors.New("jwtx: token expired")
)

// Claims are the access token claims of platform conventions §5.1.
type Claims struct {
	Subject       uuid.UUID
	SessionID     uuid.UUID
	Roles         []string
	EmailVerified bool
	AuthTime      time.Time
	IssuedAt      time.Time
	Expires       time.Time
	ID            string
}

// Verifier checks access tokens.
type Verifier struct {
	keys     KeySource
	issuer   string
	audience string
	clock    func() time.Time
}

// NewVerifier returns a Verifier accepting tokens of issuer for audience.
func NewVerifier(keys KeySource, issuer, audience string) *Verifier {
	return &Verifier{keys: keys, issuer: issuer, audience: audience, clock: time.Now}
}

type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
}

func parseHeader(token string) (header, error) {
	encoded, _, ok := strings.Cut(token, ".")
	if !ok {
		return header{}, ErrInvalid
	}

	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return header{}, ErrInvalid
	}

	var h header
	if err := json.Unmarshal(raw, &h); err != nil {
		return header{}, ErrInvalid
	}

	return h, nil
}

// Verify checks the header, signature, iss, aud and exp of token and the presence of sub, iat,
// exp and jti, and returns its claims; ErrKeysUnavailable when the key set cannot be fetched.
func (v *Verifier) Verify(ctx context.Context, token string) (*Claims, error) {
	h, err := parseHeader(token)
	if err != nil {
		return nil, err
	}

	if h.Alg != jwa.EdDSA().String() || h.Kid == "" {
		return nil, ErrInvalid
	}

	key, err := v.keys.Key(ctx, h.Kid)

	switch {
	case errors.Is(err, ErrKeysUnavailable):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if !isEd25519(key) {
		return nil, ErrInvalid
	}

	parsed, err := jwt.Parse([]byte(token),
		jwt.WithKey(jwa.EdDSA(), key),
		jwt.WithValidate(true),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithAcceptableSkew(_clockSkew),
		jwt.WithClock(jwt.ClockFunc(v.clock)),
		jwt.WithRequiredClaim(jwt.SubjectKey),
		jwt.WithRequiredClaim(jwt.IssuedAtKey),
		jwt.WithRequiredClaim(jwt.ExpirationKey),
		jwt.WithRequiredClaim(jwt.JwtIDKey),
	)

	switch {
	case errors.Is(err, jwt.TokenExpiredError()):
		return nil, ErrExpired
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if !singleAudience(token) {
		return nil, fmt.Errorf("%w: aud", ErrInvalid)
	}

	return claimsOf(parsed)
}

// singleAudience reports whether the aud claim of the verified token is one string.
func singleAudience(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != _jwsParts {
		return false
	}

	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}

	var payload struct {
		Aud json.RawMessage `json:"aud"`
	}

	if json.Unmarshal(raw, &payload) != nil {
		return false
	}

	var aud string

	return json.Unmarshal(payload.Aud, &aud) == nil
}

func isEd25519(key jwk.Key) bool {
	if key.KeyType() != jwa.OKP() {
		return false
	}

	okp, ok := key.(jwk.OKPPublicKey)
	if !ok {
		return false
	}

	crv, ok := okp.Crv()

	return ok && crv == jwa.Ed25519()
}

func claimsOf(t jwt.Token) (*Claims, error) {
	var (
		c        Claims
		sub, sid string
		authTime float64
	)

	sub, _ = t.Subject()
	c.ID, _ = t.JwtID()
	c.IssuedAt, _ = t.IssuedAt()
	c.Expires, _ = t.Expiration()

	if err := t.Get("sid", &sid); err != nil {
		return nil, fmt.Errorf("%w: sid", ErrInvalid)
	}

	if err := t.Get("auth_time", &authTime); err != nil {
		return nil, fmt.Errorf("%w: auth_time", ErrInvalid)
	}

	var roles []any
	if err := t.Get("roles", &roles); err != nil {
		return nil, fmt.Errorf("%w: roles", ErrInvalid)
	}

	for _, r := range roles {
		role, ok := r.(string)
		if !ok {
			return nil, fmt.Errorf("%w: roles", ErrInvalid)
		}

		c.Roles = append(c.Roles, role)
	}

	if err := t.Get("email_verified", &c.EmailVerified); err != nil {
		return nil, fmt.Errorf("%w: email_verified", ErrInvalid)
	}

	var err error

	if c.Subject, err = uuid.Parse(sub); err != nil {
		return nil, fmt.Errorf("%w: sub", ErrInvalid)
	}

	if c.SessionID, err = uuid.Parse(sid); err != nil {
		return nil, fmt.Errorf("%w: sid", ErrInvalid)
	}

	c.AuthTime = time.Unix(int64(authTime), 0)

	return &c, nil
}
