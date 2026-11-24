//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

const (
	_readyTimeout = 60 * time.Second
	_pollInterval = 500 * time.Millisecond
	// _issuer, _audience and the key seed match AUTH_* of docker/docker-compose.ci.yaml.
	_issuer       = "go-template-e2e"
	_audience     = "go-template-e2e"
	_metricsToken = "ci-metrics-token-0123456789" //nolint:gosec // CI token
)

func baseURL() string {
	if u := os.Getenv("APP_URL"); u != "" {
		return u
	}

	return "http://localhost:3000"
}

func waitReady() bool {
	ctx, cancel := context.WithTimeout(context.Background(), _readyTimeout)
	defer cancel()

	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL()+"/health/ready", http.NoBody)
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()

			if res.StatusCode == http.StatusOK {
				return true
			}
		}

		time.Sleep(_pollInterval)
	}

	return false
}

func TestMain(m *testing.M) {
	if !waitReady() {
		_, _ = os.Stderr.WriteString("service at " + baseURL() + " did not become ready\n")

		os.Exit(1)
	}

	os.Exit(m.Run())
}

type response struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

func call(t *testing.T, method, path, token, body string) *response {
	t.Helper()

	return callAt(t, baseURL(), method, path, token, body)
}

// callAt sends a request to the service at base.
func callAt(t *testing.T, base, method, path, token, body string) *response {
	t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}

	req, err := http.NewRequestWithContext(t.Context(), method, base+path, reader)
	require.NoError(t, err)

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	out := &response{status: res.StatusCode, header: res.Header, raw: raw, body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.body)

	return out
}

// keyFromSeed is the Ed25519 key whose seed is the 32 bytes from first on.
func keyFromSeed(first byte) ed25519.PrivateKey {
	seed := make([]byte, ed25519.SeedSize)
	for i := range seed {
		seed[i] = first + byte(i)
	}

	return ed25519.NewKeyFromSeed(seed)
}

// signingKey is AUTH_SIGNING_KEY of docker/docker-compose.ci.yaml: the seed is the bytes 1..32.
func signingKey() ed25519.PrivateKey {
	return keyFromSeed(1)
}

// oldKey is the private key of AUTH_VERIFY_KEYS of docker/docker-compose.ci.yaml: bytes 33..64.
func oldKey() ed25519.PrivateKey {
	return keyFromSeed(33)
}

func token(t *testing.T) string {
	t.Helper()

	return tokenSignedBy(t, signingKey())
}

func tokenSignedBy(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()

	signer, err := jwtx.NewSigner(key, _issuer, _audience)
	require.NoError(t, err)

	iat, exp := jwtx.Lifetime(time.Now(), 15*time.Minute)

	signed, err := signer.Sign(&jwtx.Claims{
		Subject: uuid.NewV7(), SessionID: uuid.NewV7(), Roles: []string{"user"}, EmailVerified: false,
		AuthTime: iat, IssuedAt: iat, Expires: exp, ID: uuid.NewV7().String(),
	})
	require.NoError(t, err)

	return signed
}

func TestProbes(t *testing.T) {
	t.Parallel()

	live := call(t, http.MethodGet, "/health/live", "", "")
	assert.Equal(t, http.StatusOK, live.status)

	ready := call(t, http.MethodGet, "/health/ready", "", "")
	require.Equal(t, http.StatusOK, ready.status)
	assert.Equal(t, map[string]any{"database": "ok", "redis": "ok"}, ready.body["checks"])
}

func TestMetricsNeedTheTokenInProduction(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusUnauthorized, call(t, http.MethodGet, "/metrics", "", "").status)

	r := call(t, http.MethodGet, "/metrics", _metricsToken, "")
	require.Equal(t, http.StatusOK, r.status)
	assert.Contains(t, string(r.raw), "http_server_request_duration_seconds")
}

func TestOpenAPIJSONIsTheCommittedDocument(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(filepath.Join("..", "..", "openapi", "openapi.json"))
	require.NoError(t, err)

	r := call(t, http.MethodGet, "/openapi.json", "", "")
	require.Equal(t, http.StatusOK, r.status)
	assert.True(t, bytes.Equal(want, r.raw))
}

func TestUnknownRouteIsTheErrorEnvelope(t *testing.T) {
	t.Parallel()

	r := call(t, http.MethodGet, "/api/v1/nope", "", "")
	assert.Equal(t, http.StatusNotFound, r.status)
	assert.Equal(t, "common.notFound", r.body["errorKey"])
	assert.InDelta(t, 40400, r.body["code"], 0)
	assert.Equal(t, r.header.Get("X-Request-Id"), r.body["requestId"])
}

func TestNotes(t *testing.T) {
	t.Parallel()

	tok := token(t)

	assert.Equal(t, http.StatusUnauthorized, call(t, http.MethodGet, "/api/v1/notes", "", "").status)

	created := call(t, http.MethodPost, "/api/v1/notes", tok, `{"title":"e2e","content":"hello"}`)
	require.Equal(t, http.StatusCreated, created.status, string(created.raw))

	data, _ := created.body["data"].(map[string]any)
	id, _ := data["id"].(string)

	got := call(t, http.MethodGet, "/api/v1/notes/"+id, tok, "")
	assert.Equal(t, http.StatusOK, got.status)

	list := call(t, http.MethodGet, "/api/v1/notes", tok, "")
	require.Equal(t, http.StatusOK, list.status)
	assert.Contains(t, string(list.raw), id)

	assert.Equal(t, http.StatusOK, call(t, http.MethodDelete, "/api/v1/notes/"+id, tok, "").status)
	assert.Equal(t, http.StatusNotFound, call(t, http.MethodGet, "/api/v1/notes/"+id, tok, "").status)
}
