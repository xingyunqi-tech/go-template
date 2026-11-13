//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	_readyTimeout = 60 * time.Second
	_pollInterval = 500 * time.Millisecond
)

func baseURL() string {
	if u := os.Getenv("APP_URL"); u != "" {
		return u
	}

	return "http://localhost:3000"
}

func get(ctx context.Context, path string) (status int, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL()+path, http.NoBody)
	if err != nil {
		return 0, nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)

	return resp.StatusCode, body, err
}

func waitReady() bool {
	ctx, cancel := context.WithTimeout(context.Background(), _readyTimeout)
	defer cancel()

	for ctx.Err() == nil {
		if status, _, err := get(ctx, "/"); err == nil && status == http.StatusOK {
			return true
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

func TestAppInfo(t *testing.T) {
	t.Parallel()

	status, body, err := get(t.Context(), "/")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"code":200,"message":"success","data":{"name":"app"}}`, string(body))
}

func TestDocsJSONIsBaseline(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile(filepath.Join("..", "..", "openapi", "nestjs-baseline.json"))
	require.NoError(t, err)

	status, body, err := get(t.Context(), "/docs-json")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.True(t, bytes.Equal(want, body), "/docs-json differs from the baseline")
}

func TestUnknownRouteIsNestStyle404(t *testing.T) {
	t.Parallel()

	status, body, err := get(t.Context(), "/api/v1/nope")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, status)
	assert.JSONEq(t, `{"code":404,"message":"Cannot GET /api/v1/nope","data":null}`, string(body))
}

func TestSimpleHealthCheck(t *testing.T) {
	t.Parallel()

	status, body, err := get(t.Context(), "/api/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), `"status":"ok"`)
}
