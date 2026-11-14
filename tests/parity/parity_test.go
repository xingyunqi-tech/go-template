//go:build parity

// Package parity_test sends the same requests to nestjs-template and this service and compares
// the responses (fastapi-temp tests/parity, case for case).
package parity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const _requestTimeout = 10 * time.Second

// rateLimitHeaders appear under the same conditions on both sides; values may differ.
func rateLimitHeaders() []string {
	return []string{"X-Ratelimit-Limit", "X-Ratelimit-Remaining", "X-Ratelimit-Reset", "Retry-After"}
}

var _clientIP atomic.Uint32 //nolint:gochecknoglobals // per-run X-Forwarded-For counter shared by all cases

func init() { //nolint:gochecknoinits // seeds the X-Forwarded-For counter once per run
	_clientIP.Store(rand.Uint32N(1 << 24)) //nolint:gosec // address spread, not security
}

func baseURL(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return strings.TrimSuffix(v, "/")
	}

	return def
}

// response is a fully read HTTP response.
type response struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r *response) JSON(t *testing.T) any {
	t.Helper()

	var v any
	require.NoError(t, json.Unmarshal(r.Body, &v), string(r.Body))

	return v
}

// pair holds the two responses to one request: Go first, nestjs second.
type pair struct {
	Go     *response
	Nestjs *response
}

type requestOptions struct {
	headers map[string]string
	body    []byte
	// sides holds per-service headers and targets: index 0 is Go, 1 nestjs.
	sides [2]side
}

type side struct {
	headers map[string]string
	target  string
}

type option func(*requestOptions)

func withHeader(name, value string) option {
	return func(o *requestOptions) { o.headers[name] = value }
}

// withTokens authenticates each side with its own access token.
func withTokens(tokens [2]string) option {
	return func(o *requestOptions) {
		for i := range o.sides {
			o.sides[i].headers["Authorization"] = "Bearer " + tokens[i]
		}
	}
}

// withTargets sends each side to its own path.
func withTargets(targets [2]string) option {
	return func(o *requestOptions) {
		for i := range o.sides {
			o.sides[i].target = targets[i]
		}
	}
}

func withJSON(t *testing.T, v any) option {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return withBody("application/json", raw)
}

func withBody(contentType string, body []byte) option {
	return func(o *requestOptions) {
		o.headers["Content-Type"] = contentType
		o.body = body
	}
}

func send(t *testing.T, base, method, target string, opts *requestOptions, s side) *response {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), _requestTimeout)
	defer cancel()

	var body io.Reader
	if opts.body != nil {
		body = bytes.NewReader(opts.body)
	}

	if s.target != "" {
		target = s.target
	}

	req, err := http.NewRequestWithContext(ctx, method, base+target, body)
	require.NoError(t, err)

	n := _clientIP.Add(1) % (1 << 24)
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.%d.%d.%d", n>>16, (n>>8)&0xFF, n&0xFF))

	for k, v := range opts.headers {
		req.Header.Set(k, v)
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, res.Body.Close()) }()

	data, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return &response{Status: res.StatusCode, Header: res.Header, Body: data}
}

// do sends the request to both services and checks the throttling conventions: no 429 and the same
// set of integer rate-limit headers with equal limits.
func do(t *testing.T, method, target string, options ...option) pair {
	t.Helper()

	opts := &requestOptions{
		headers: map[string]string{}, body: nil,
		sides: [2]side{{headers: map[string]string{}, target: ""}, {headers: map[string]string{}, target: ""}},
	}
	for _, o := range options {
		o(opts)
	}

	p := pair{
		Go:     send(t, baseURL("PARITY_GO_URL", "http://localhost:3000"), method, target, opts, opts.sides[0]),
		Nestjs: send(t, baseURL("PARITY_NESTJS_URL", "http://localhost:3001"), method, target, opts, opts.sides[1]),
	}

	for _, r := range []*response{p.Go, p.Nestjs} {
		require.NotEqual(t, http.StatusTooManyRequests, r.Status, "%s %s: %s", method, target, r.Body)
	}

	requireSameRateLimitHeaders(t, p)

	return p
}

func requireSameRateLimitHeaders(t *testing.T, p pair) {
	t.Helper()

	present := func(r *response) []string {
		var names []string

		for _, name := range rateLimitHeaders() {
			if v := r.Header.Get(name); v != "" {
				names = append(names, name)

				for _, c := range v {
					require.True(t, c >= '0' && c <= '9', "%s: %q", name, v)
				}
			}
		}

		return names
	}

	require.Equal(t, present(p.Nestjs), present(p.Go))
	require.Equal(t, p.Nestjs.Header.Get("X-Ratelimit-Limit"), p.Go.Header.Get("X-Ratelimit-Limit"))
}
