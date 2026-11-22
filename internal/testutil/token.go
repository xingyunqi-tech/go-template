//go:build integration

package testutil

import (
	"crypto/ed25519"
	"testing"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

// Issuer and Audience are the AUTH_ISSUER and AUTH_AUDIENCE of the test configuration.
const (
	Issuer   = "go-template-test"
	Audience = "go-template-test"
)

const _accessTTL = 15 * time.Minute

// signingKey is the fixed Ed25519 test key.
func signingKey() ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = byte(i + 1)
	}

	return ed25519.NewKeyFromSeed(seed)
}

// SigningKeyPEM is the AUTH_SIGNING_KEY of the test key.
func SigningKeyPEM() string {
	pemText, err := jwtx.EncodePrivateKey(signingKey())
	if err != nil {
		panic(err)
	}

	return pemText
}

// TokenOptions are the claims of IssueAccessToken; zero values become a fresh user who signed in
// now with an unverified email.
type TokenOptions struct {
	Subject       uuid.UUID
	SessionID     uuid.UUID
	Roles         []string
	EmailVerified bool
	AuthTime      time.Time
	TTL           time.Duration
}

// IssueAccessToken signs an access token with the test key.
func IssueAccessToken(t *testing.T, opts *TokenOptions) string {
	t.Helper()

	if opts == nil {
		opts = &TokenOptions{} //nolint:exhaustruct // defaults below
	}

	if opts.Subject == uuid.Nil() {
		opts.Subject = uuid.NewV7()
	}

	if opts.SessionID == uuid.Nil() {
		opts.SessionID = uuid.NewV7()
	}

	if opts.Roles == nil {
		opts.Roles = []string{"user"}
	}

	if opts.AuthTime.IsZero() {
		opts.AuthTime = time.Now()
	}

	if opts.TTL == 0 {
		opts.TTL = _accessTTL
	}

	signer, err := jwtx.NewSigner(signingKey(), Issuer, Audience)
	if err != nil {
		t.Fatalf("testutil: signer: %v", err)
	}

	iat, exp := jwtx.Lifetime(time.Now(), opts.TTL)

	token, err := signer.Sign(&jwtx.Claims{
		Subject: opts.Subject, SessionID: opts.SessionID, Roles: opts.Roles, EmailVerified: opts.EmailVerified,
		AuthTime: opts.AuthTime, IssuedAt: iat, Expires: exp, ID: uuid.NewV7().String(),
	})
	if err != nil {
		t.Fatalf("testutil: sign: %v", err)
	}

	return token
}
