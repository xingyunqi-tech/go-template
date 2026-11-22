package password_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/password"
)

func fast() password.Params {
	return password.Params{Memory: 64, Time: 1, Threads: 1}
}

func TestHashIsAnArgon2idPHCString(t *testing.T) {
	t.Parallel()

	h, err := password.New(password.Default(), 1)
	require.NoError(t, err)

	encoded, err := h.Hash(t.Context(), "Passw0rd!")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$"), encoded)

	ok, rehash, err := h.Verify(t.Context(), encoded, "Passw0rd!")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.False(t, rehash)

	ok, _, err = h.Verify(t.Context(), encoded, "wrong")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestOtherParametersAskForARehash(t *testing.T) {
	t.Parallel()

	old, err := password.New(fast(), 1)
	require.NoError(t, err)

	encoded, err := old.Hash(t.Context(), "pw")
	require.NoError(t, err)

	current, err := password.New(password.Default(), 1)
	require.NoError(t, err)

	ok, rehash, err := current.Verify(t.Context(), encoded, "pw")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, rehash)
}

func TestMissingHashNeverMatches(t *testing.T) {
	t.Parallel()

	h, err := password.New(fast(), 1)
	require.NoError(t, err)

	ok, rehash, err := h.Verify(t.Context(), "", "anything")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, rehash)

	_, _, err = h.Verify(t.Context(), "$bcrypt$x", "anything")
	require.Error(t, err)
}
