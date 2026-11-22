// Package password hashes passwords with argon2id as PHC strings (platform conventions §5.4).
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Params are the argon2id parameters.
type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
}

// Default is m=19456 KiB, t=2, p=1.
func Default() Params {
	return Params{Memory: _memoryKiB, Time: _passes, Threads: 1}
}

const (
	_memoryKiB = 19456
	_passes    = 2

	_saltLen = 16
	_keyLen  = 32
	_parts   = 6
)

var errMalformed = errors.New("password: malformed hash")

// Hasher hashes and verifies passwords, at most concurrency at a time.
type Hasher struct {
	params Params
	slots  chan struct{}
	dummy  string
}

// New returns a Hasher; it computes the dummy hash verified for unknown accounts.
func New(params Params, concurrency int) (*Hasher, error) {
	h := &Hasher{params: params, slots: make(chan struct{}, max(concurrency, 1)), dummy: ""}

	dummy, err := h.hash("dummy-password-for-unknown-accounts")
	if err != nil {
		return nil, err
	}

	h.dummy = dummy

	return h, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("password: %w", ctx.Err())
	}
}

func (h *Hasher) release() { <-h.slots }

// Hash returns the PHC string of password.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()

	return h.hash(password)
}

func (h *Hasher) hash(password string) (string, error) {
	salt := make([]byte, _saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.Memory, h.params.Threads, _keyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, h.params.Memory, h.params.Time,
		h.params.Threads, b64(salt), b64(key)), nil
}

// Verify reports whether password matches encoded and whether encoded uses other parameters
// than the Hasher's; an empty encoded is checked against the dummy hash and never matches.
func (h *Hasher) Verify(ctx context.Context, encoded, password string) (ok, rehash bool, err error) {
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()

	if encoded == "" {
		_, _, _ = verify(h.dummy, password)

		return false, false, nil
	}

	ok, params, err := verify(encoded, password)
	if err != nil {
		return false, false, err
	}

	return ok, ok && params != h.params, nil
}

func verify(encoded, password string) (bool, Params, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != _parts || parts[1] != "argon2id" {
		return false, Params{}, errMalformed
	}

	var (
		version int
		p       Params
	)

	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, Params{}, errMalformed
	}

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return false, Params{}, errMalformed
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, Params{}, errMalformed
	}

	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, Params{}, errMalformed
	}

	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want))) //nolint:gosec // key length from a decoded hash

	return subtle.ConstantTimeCompare(got, want) == 1, p, nil
}

func b64(b []byte) string {
	return base64.RawStdEncoding.EncodeToString(b)
}
