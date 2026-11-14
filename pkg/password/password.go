// Package password hashes passwords with argon2id or bcrypt, verifies both formats and reports
// when a stored hash should be replaced with one in the preferred format.
package password

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Algorithms accepted by New.
const (
	Argon2id = "argon2id"
	Bcrypt   = "bcrypt"
)

const (
	_argonMemoryKiB = 19_456
	_argonTime      = 2
	_argonThreads   = 1
	_argonSaltLen   = 16
	_argonKeyLen    = 32
	_argonVersion   = 19
	_bcryptCost     = 10
	_argonPrefix    = "$argon2id$"
	_phcParts       = 6
)

var (
	// ErrUnknownAlgorithm reports an algorithm other than argon2id and bcrypt.
	ErrUnknownAlgorithm = errors.New("password: unknown algorithm")
	errMalformedHash    = errors.New("password: malformed argon2id hash")
)

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func defaultArgon() argonParams {
	return argonParams{memory: _argonMemoryKiB, time: _argonTime, threads: _argonThreads}
}

// Hasher hashes and verifies passwords; at most `concurrency` operations run at once.
type Hasher struct {
	algorithm string
	slots     chan struct{}
}

// New returns a Hasher producing algorithm hashes.
func New(algorithm string, concurrency int) (*Hasher, error) {
	if algorithm != Argon2id && algorithm != Bcrypt {
		return nil, fmt.Errorf("%w: %s", ErrUnknownAlgorithm, algorithm)
	}

	return &Hasher{algorithm: algorithm, slots: make(chan struct{}, max(concurrency, 1))}, nil
}

func (h *Hasher) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("password: %w", err)
	}

	select {
	case h.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("password: %w", ctx.Err())
	}
}

func (h *Hasher) release() {
	<-h.slots
}

// Hash hashes secret in the preferred format.
func (h *Hasher) Hash(ctx context.Context, secret string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()

	return h.hash(secret)
}

func (h *Hasher) hash(secret string) (string, error) {
	if h.algorithm == Bcrypt {
		out, err := bcrypt.GenerateFromPassword([]byte(secret), _bcryptCost)
		if err != nil {
			return "", fmt.Errorf("password: bcrypt: %w", err)
		}

		return string(out), nil
	}

	salt := make([]byte, _argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: salt: %w", err)
	}

	p := defaultArgon()
	key := argon2.IDKey([]byte(secret), salt, p.time, p.memory, p.threads, _argonKeyLen)

	return fmt.Sprintf("%sv=%d$m=%d,t=%d,p=%d$%s$%s",
		_argonPrefix, _argonVersion, p.memory, p.time, p.threads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyAndUpdate reports whether one of the candidates matches stored; when it does and stored
// is not in the preferred format or parameters, updated is a fresh hash of the matching candidate.
func (h *Hasher) VerifyAndUpdate(ctx context.Context, stored string, candidates ...string) (ok bool, updated string, err error) {
	if err := h.acquire(ctx); err != nil {
		return false, "", err
	}
	defer h.release()

	for _, candidate := range candidates {
		if !verify(stored, candidate) {
			continue
		}

		if !h.needsRehash(stored) {
			return true, "", nil
		}

		fresh, err := h.hash(candidate)
		if err != nil {
			return true, "", err
		}

		return true, fresh, nil
	}

	return false, "", nil
}

func (h *Hasher) needsRehash(stored string) bool {
	if h.algorithm == Bcrypt {
		cost, err := bcrypt.Cost([]byte(stored))

		return err != nil || cost != _bcryptCost
	}

	p, _, _, err := parseArgon(stored)

	return err != nil || p != defaultArgon()
}

func verify(stored, candidate string) bool {
	if strings.HasPrefix(stored, _argonPrefix) {
		p, salt, key, err := parseArgon(stored)
		if err != nil {
			return false
		}

		got := argon2.IDKey([]byte(candidate), salt, p.time, p.memory, p.threads, uint32(len(key))) //nolint:gosec // decoded hash lengths are far below uint32

		return subtle.ConstantTimeCompare(got, key) == 1
	}

	return bcrypt.CompareHashAndPassword([]byte(stored), []byte(candidate)) == nil
}

func parseArgon(stored string) (p argonParams, salt, key []byte, err error) {
	parts := strings.Split(stored, "$")
	if len(parts) != _phcParts || parts[2] != "v="+strconv.Itoa(_argonVersion) {
		return argonParams{}, nil, nil, errMalformedHash
	}

	var threads uint32

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &threads); err != nil {
		return argonParams{}, nil, nil, errMalformedHash
	}

	p.threads = uint8(min(threads, math.MaxUint8))

	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, errMalformedHash
	}

	key, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return argonParams{}, nil, nil, errMalformedHash
	}

	return p, salt, key, nil
}
