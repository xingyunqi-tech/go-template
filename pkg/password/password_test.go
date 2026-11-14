package password_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/password"
)

const (
	_fastapiArgon  = "$argon2id$v=19$m=19456,t=2,p=1$eZFQjApEiJCkKkGSGCeAoQ$UKqWYrqsQ8w0fDXPHFnTA/o4pElv1dwsVXI0FLCY/JY"
	_fastapiBcrypt = "$2b$10$VIMqZdNkh1rdNkYKg6pMJOr6L7qAhuFOCizmBUkI1fSPPwbSajbKS"
)

func TestHashRoundTripsInEitherFormat(t *testing.T) {
	t.Parallel()

	for _, algorithm := range []string{password.Argon2id, password.Bcrypt} {
		h, err := password.New(algorithm, 2)
		require.NoError(t, err)

		hash, err := h.Hash(t.Context(), "secret")
		require.NoError(t, err)

		ok, updated, err := h.VerifyAndUpdate(t.Context(), hash, "wrong", "secret")
		require.NoError(t, err)
		assert.True(t, ok, algorithm)
		assert.Empty(t, updated, algorithm)

		ok, _, err = h.VerifyAndUpdate(t.Context(), hash, "wrong")
		require.NoError(t, err)
		assert.False(t, ok, algorithm)
	}
}

func TestVerifiesHashesFromOtherImplementations(t *testing.T) {
	t.Parallel()

	h, err := password.New(password.Argon2id, 1)
	require.NoError(t, err)

	ok, updated, err := h.VerifyAndUpdate(t.Context(), _fastapiArgon, "abc")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Empty(t, updated)

	ok, updated, err = h.VerifyAndUpdate(t.Context(), _fastapiBcrypt, "abc")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, strings.HasPrefix(updated, "$argon2id$v=19$m=19456,t=2,p=1$"))
}

func TestRehashesOutdatedParameters(t *testing.T) {
	t.Parallel()

	argon, err := password.New(password.Argon2id, 1)
	require.NoError(t, err)

	bcryptHasher, err := password.New(password.Bcrypt, 1)
	require.NoError(t, err)

	weak := "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$" + "aNRXl6n1i9HB4yZHFBUEZ2eFhbiiXfNYjwqZoOU3Qms"
	ok, _, err := argon.VerifyAndUpdate(t.Context(), weak, "abc")
	require.NoError(t, err)
	assert.False(t, ok)

	hash, err := argon.Hash(t.Context(), "abc")
	require.NoError(t, err)

	ok, updated, err := bcryptHasher.VerifyAndUpdate(t.Context(), hash, "abc")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, strings.HasPrefix(updated, "$2a$10$"))
}

func TestRejectsMalformedHashes(t *testing.T) {
	t.Parallel()

	h, err := password.New(password.Argon2id, 1)
	require.NoError(t, err)

	for _, stored := range []string{"", "plain", "$argon2id$v=18$m=1,t=1,p=1$a$b", "$argon2id$v=19$x$a$b", "$argon2id$v=19$m=1,t=1,p=1$!!$b"} {
		ok, _, err := h.VerifyAndUpdate(t.Context(), stored, "abc")
		require.NoError(t, err)
		assert.False(t, ok, stored)
	}

	_, err = password.New("md5", 1)
	require.ErrorIs(t, err, password.ErrUnknownAlgorithm)
}

func TestConcurrencyLimitHonorsContext(t *testing.T) {
	t.Parallel()

	h, err := password.New(password.Bcrypt, 1)
	require.NoError(t, err)

	hash, err := h.Hash(t.Context(), "abc")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), time.Nanosecond)
	defer cancel()

	<-ctx.Done()

	_, err = h.Hash(ctx, "abc")
	assert.Error(t, err)

	_, _, err = h.VerifyAndUpdate(ctx, hash, "abc")
	assert.Error(t, err)
}
