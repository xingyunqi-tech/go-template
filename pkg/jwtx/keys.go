// Package jwtx holds the Ed25519 signing keys and verifies EdDSA access tokens (platform
// conventions §5.1).
package jwtx

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lestrrat-go/httprc/v3"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
)

const _minRefreshInterval = 30 * time.Second

// ErrKeysUnavailable reports a remote key set that could not be fetched.
var ErrKeysUnavailable = errors.New("jwtx: key set unavailable")

var (
	errNoPEM      = errors.New("jwtx: no PEM block")
	errNotEd25519 = errors.New("jwtx: key is not Ed25519")
	errUnknownKey = errors.New("jwtx: unknown kid")
)

// KeyID is the RFC 7638 SHA-256 thumbprint of pub, base64url without padding.
func KeyID(pub ed25519.PublicKey) (string, error) {
	key, err := jwk.Import(pub)
	if err != nil {
		return "", fmt.Errorf("jwtx: %w", err)
	}

	sum, err := key.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("jwtx: thumbprint: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(sum), nil
}

// ParsePrivateKey reads a PKCS #8 PEM Ed25519 private key.
func ParsePrivateKey(pemText string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(pemText)))
	if block == nil {
		return nil, errNoPEM
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("jwtx: private key: %w", err)
	}

	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errNotEd25519
	}

	return priv, nil
}

// ParsePublicKeys reads every PKIX PEM Ed25519 public key in pemText.
func ParsePublicKeys(pemText string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey

	rest := []byte(strings.TrimSpace(pemText))
	for len(rest) > 0 {
		var block *pem.Block

		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, errNoPEM
		}

		key, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("jwtx: public key: %w", err)
		}

		pub, ok := key.(ed25519.PublicKey)
		if !ok {
			return nil, errNotEd25519
		}

		keys = append(keys, pub)
	}

	return keys, nil
}

// EncodePrivateKey writes priv as PKCS #8 PEM.
func EncodePrivateKey(priv ed25519.PrivateKey) (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", fmt.Errorf("jwtx: %w", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

// EncodePublicKey writes pub as PKIX PEM.
func EncodePublicKey(pub ed25519.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", fmt.Errorf("jwtx: %w", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), nil
}

// PublicJWK is pub as an OKP JWK with kid, alg EdDSA and use sig.
func PublicJWK(pub ed25519.PublicKey) (jwk.Key, error) {
	key, err := jwk.Import(pub)
	if err != nil {
		return nil, fmt.Errorf("jwtx: %w", err)
	}

	kid, err := KeyID(pub)
	if err != nil {
		return nil, err
	}

	for name, value := range map[string]any{jwk.KeyIDKey: kid, jwk.AlgorithmKey: jwa.EdDSA(), jwk.KeyUsageKey: jwk.ForSignature} {
		if err := key.Set(name, value); err != nil {
			return nil, fmt.Errorf("jwtx: %w", err)
		}
	}

	return key, nil
}

// KeySource finds the verification key of a kid.
type KeySource interface {
	Key(ctx context.Context, kid string) (jwk.Key, error)
}

// StaticKeys is the key set of the issuing service: the current key and the rotated keys still
// accepted.
type StaticKeys struct {
	set jwk.Set
}

// NewStaticKeys returns the set of pubs.
func NewStaticKeys(pubs ...ed25519.PublicKey) (*StaticKeys, error) {
	set := jwk.NewSet()

	for _, pub := range pubs {
		key, err := PublicJWK(pub)
		if err != nil {
			return nil, err
		}

		if err := set.AddKey(key); err != nil {
			return nil, fmt.Errorf("jwtx: %w", err)
		}
	}

	return &StaticKeys{set: set}, nil
}

// Set is the JWKS document.
func (s *StaticKeys) Set() jwk.Set {
	return s.set
}

// Key implements KeySource.
func (s *StaticKeys) Key(_ context.Context, kid string) (jwk.Key, error) {
	key, ok := s.set.LookupKeyID(kid)
	if !ok {
		return nil, errUnknownKey
	}

	return key, nil
}

// RemoteKeys is the key set at AUTH_JWKS_URL, fetched in the background; an unknown kid
// refreshes it, at most once every 30 seconds.
type RemoteKeys struct {
	cache *jwk.Cache
	url   string
	now   func() time.Time

	mu        sync.Mutex
	refreshed time.Time
}

// NewRemoteKeys registers url without waiting for the first fetch.
func NewRemoteKeys(ctx context.Context, url string) (*RemoteKeys, error) {
	cache, err := jwk.NewCache(ctx, httprc.NewClient())
	if err != nil {
		return nil, fmt.Errorf("jwtx: %w", err)
	}

	if err := cache.Register(ctx, url, jwk.WithWaitReady(false), jwk.WithMinInterval(_minRefreshInterval)); err != nil {
		return nil, fmt.Errorf("jwtx: %w", err)
	}

	return &RemoteKeys{cache: cache, url: url, now: time.Now, mu: sync.Mutex{}, refreshed: time.Time{}}, nil
}

// Key implements KeySource.
func (r *RemoteKeys) Key(ctx context.Context, kid string) (jwk.Key, error) {
	set, err := r.cache.Lookup(ctx, r.url)
	if err == nil {
		if key, ok := set.LookupKeyID(kid); ok {
			return key, nil
		}
	}

	fetched := err == nil

	if !r.mayRefresh() {
		if !fetched {
			return nil, ErrKeysUnavailable
		}

		return nil, errUnknownKey
	}

	set, err = r.cache.Refresh(ctx, r.url)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrKeysUnavailable, err)
	}

	key, ok := set.LookupKeyID(kid)
	if !ok {
		return nil, errUnknownKey
	}

	return key, nil
}

func (r *RemoteKeys) mayRefresh() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if now.Sub(r.refreshed) < _minRefreshInterval {
		return false
	}

	r.refreshed = now

	return true
}
