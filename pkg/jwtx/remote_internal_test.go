package jwtx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func publicOf(t *testing.T) (pub ed25519.PublicKey, kid string) {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	kid, err = KeyID(pub)
	require.NoError(t, err)

	return pub, kid
}

func TestRemoteKeysRefreshOnUnknownKidAtMostEvery30Seconds(t *testing.T) {
	t.Parallel()

	first, firstKid := publicOf(t)
	second, secondKid := publicOf(t)

	var current atomic.Pointer[StaticKeys]

	keys, err := NewStaticKeys(first)
	require.NoError(t, err)
	current.Store(keys)

	var fetches atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(current.Load().Set())
	}))
	t.Cleanup(srv.Close)

	remote, err := NewRemoteKeys(t.Context(), srv.URL)
	require.NoError(t, err)

	now := time.Now()
	remote.now = func() time.Time { return now }

	_, err = remote.Key(t.Context(), firstKid)
	require.NoError(t, err)

	rotated, err := NewStaticKeys(first, second)
	require.NoError(t, err)
	current.Store(rotated)

	now = now.Add(10 * time.Second)
	before := fetches.Load()

	_, err = remote.Key(t.Context(), secondKid)
	require.ErrorIs(t, err, errUnknownKey)
	assert.Equal(t, before, fetches.Load())

	now = now.Add(30 * time.Second)

	_, err = remote.Key(t.Context(), secondKid)
	require.NoError(t, err)
	assert.Greater(t, fetches.Load(), before)
}

func TestRemoteKeysReportAnUnreachableKeySetAsUnavailable(t *testing.T) {
	t.Parallel()

	_, kid := publicOf(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	remote, err := NewRemoteKeys(t.Context(), srv.URL)
	require.NoError(t, err)

	now := time.Now()
	remote.now = func() time.Time { return now }

	_, err = remote.Key(t.Context(), kid)
	require.ErrorIs(t, err, ErrKeysUnavailable)

	_, err = remote.Key(t.Context(), kid)
	require.ErrorIs(t, err, ErrKeysUnavailable)
}

func TestRemoteKeysReportAFailedRefreshAsUnavailable(t *testing.T) {
	t.Parallel()

	pub, kid := publicOf(t)
	_, unknown := publicOf(t)

	keys, err := NewStaticKeys(pub)
	require.NoError(t, err)

	var down atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(keys.Set())
	}))
	t.Cleanup(srv.Close)

	remote, err := NewRemoteKeys(t.Context(), srv.URL)
	require.NoError(t, err)

	now := time.Now()
	remote.now = func() time.Time { return now }

	_, err = remote.Key(t.Context(), kid)
	require.NoError(t, err)

	down.Store(true)

	now = now.Add(time.Minute)

	_, err = remote.Key(t.Context(), unknown)
	require.ErrorIs(t, err, ErrKeysUnavailable)

	_, err = remote.Key(t.Context(), kid)
	require.NoError(t, err)
}
