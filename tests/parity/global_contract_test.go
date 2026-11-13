//go:build parity

package parity_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"uuid"

	"github.com/stretchr/testify/require"
)

func securityHeaderNames() []string {
	return []string{
		"Content-Security-Policy", "Cross-Origin-Resource-Policy", "Origin-Agent-Cluster", "Referrer-Policy",
		"Strict-Transport-Security", "X-Content-Type-Options", "X-Dns-Prefetch-Control", "X-Download-Options",
		"X-Frame-Options", "X-Permitted-Cross-Domain-Policies", "X-Xss-Protection",
	}
}

func corsHeaderNames() []string {
	return []string{
		"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Expose-Headers",
		"Access-Control-Allow-Methods", "Access-Control-Allow-Headers", "Vary",
	}
}

func requireSame(t *testing.T, method, target string, options ...option) {
	t.Helper()

	p := do(t, method, target, options...)
	require.Equal(t, p.Nestjs.Status, p.Go.Status)
	require.Equal(t, p.Nestjs.Header.Get("Content-Type"), p.Go.Header.Get("Content-Type"))
	require.Equal(t, p.Nestjs.JSON(t), p.Go.JSON(t))
}

func requireSameHeaders(t *testing.T, p pair, names []string) {
	t.Helper()

	for _, name := range names {
		require.Equal(t, p.Nestjs.Header.Values(name), p.Go.Header.Values(name), name)
	}
}

func TestUnmatchedRoutes(t *testing.T) {
	t.Parallel()

	cases := [][2]string{
		{"GET", "/api/v1/nope"},
		{"GET", "/api/v1/nope?a=1%20b&c&d=%E4%B8%AD"},
		{"DELETE", "/api/nope/x"},
		{"PATCH", "/api/health?x=1"},
		{"POST", "/"},
		{"PUT", "/metrics/"},
	}

	for _, c := range cases {
		t.Run(c[0]+" "+c[1], func(t *testing.T) {
			t.Parallel()

			p := do(t, c[0], c[1])
			require.Equal(t, http.StatusNotFound, p.Nestjs.Status)
			require.Equal(t, http.StatusNotFound, p.Go.Status)
			require.Empty(t, p.Go.Header.Get("Allow"))
			require.Equal(t, p.Nestjs.JSON(t), p.Go.JSON(t))
		})
	}
}

func TestMatchedRoutes(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"/", "/api/health", "/API/HEALTH", "/api/health/", "/Api/Health/"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			p := do(t, http.MethodGet, target)
			require.Equal(t, http.StatusOK, p.Nestjs.Status)
			require.Equal(t, http.StatusOK, p.Go.Status)
			require.Equal(t, p.Nestjs.Header.Get("Content-Type"), p.Go.Header.Get("Content-Type"))

			for _, r := range []*response{p.Go, p.Nestjs} {
				body, ok := r.JSON(t).(map[string]any)
				require.True(t, ok)
				require.ElementsMatch(t, []string{"code", "message", "data"}, keys(body))
			}
		})
	}
}

func TestAppInfo(t *testing.T) {
	t.Parallel()

	requireSame(t, http.MethodGet, "/")
}

func TestHealthShape(t *testing.T) {
	t.Parallel()

	p := do(t, http.MethodGet, "/api/health")
	data := func(r *response) map[string]any {
		body, ok := r.JSON(t).(map[string]any)
		require.True(t, ok)

		d, ok := body["data"].(map[string]any)
		require.True(t, ok)

		return d
	}
	goData, nestData := data(p.Go), data(p.Nestjs)

	require.ElementsMatch(t, keys(nestData), keys(goData))
	goMemory, _ := goData["memory"].(map[string]any)
	nestMemory, _ := nestData["memory"].(map[string]any)
	require.ElementsMatch(t, keys(nestMemory), keys(goMemory))
	require.Equal(t, "ok", goData["status"])
	require.Equal(t, "ok", nestData["status"])

	goTime, _ := goData["timestamp"].(string)
	nestTime, _ := nestData["timestamp"].(string)
	require.Len(t, goTime, len(nestTime))
	require.True(t, strings.HasSuffix(goTime, "Z"))
}

func TestHead(t *testing.T) {
	t.Parallel()

	p := do(t, http.MethodHead, "/api/health")
	require.Equal(t, http.StatusOK, p.Nestjs.Status)
	require.Equal(t, http.StatusOK, p.Go.Status)
	require.Empty(t, p.Nestjs.Body)
	require.Empty(t, p.Go.Body)
}

func TestRequestIDEchoed(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"abc.1:2-3", strings.Repeat("x", 128)} {
		t.Run(id[:min(len(id), 16)], func(t *testing.T) {
			t.Parallel()

			p := do(t, http.MethodGet, "/", withHeader("X-Request-Id", id))
			require.Equal(t, id, p.Nestjs.Header.Get("X-Request-Id"))
			require.Equal(t, id, p.Go.Header.Get("X-Request-Id"))
		})
	}
}

func TestRequestIDGenerated(t *testing.T) {
	t.Parallel()

	cases := map[string][]option{
		"missing":  nil,
		"invalid":  {withHeader("X-Request-Id", "bad id!")},
		"too long": {withHeader("X-Request-Id", strings.Repeat("x", 129))},
	}

	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := do(t, http.MethodGet, "/api/v1/nope", opts...)
			for _, r := range []*response{p.Go, p.Nestjs} {
				generated := r.Header.Get("X-Request-Id")
				parsed, err := uuid.Parse(generated)
				require.NoError(t, err)
				require.Equal(t, generated, parsed.String())
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	requireSameHeaders(t, do(t, http.MethodGet, "/api/health"), securityHeaderNames())
}

func TestCORSSimpleRequest(t *testing.T) {
	t.Parallel()

	cases := map[string][]option{
		"no origin":   nil,
		"with origin": {withHeader("Origin", "http://a.example")},
	}

	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			requireSameHeaders(t, do(t, http.MethodGet, "/", opts...), corsHeaderNames())
		})
	}
}

func TestCORSPreflight(t *testing.T) {
	t.Parallel()

	cases := map[string][]option{
		"bare": nil,
		"origin and method": {
			withHeader("Origin", "http://a.example"),
			withHeader("Access-Control-Request-Method", "POST"),
		},
		"requested headers": {
			withHeader("Origin", "http://a.example"),
			withHeader("Access-Control-Request-Method", "PUT"),
			withHeader("Access-Control-Request-Headers", "authorization,content-type,x-foo"),
		},
	}

	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p := do(t, http.MethodOptions, "/api/v1/auth/me", opts...)
			require.Equal(t, http.StatusNoContent, p.Nestjs.Status)
			require.Equal(t, http.StatusNoContent, p.Go.Status)
			require.Empty(t, p.Nestjs.Body)
			require.Empty(t, p.Go.Body)
			requireSameHeaders(t, p, corsHeaderNames())
		})
	}
}

func TestInvalidJSONRejectedBeforeRouting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		method, target, body string
	}{
		{"POST", "/api/v1/nope", "x"},
		{"GET", "/", "{bad"},
		{"PATCH", "/api/health", "null"},
		{"POST", "/api/v1/nope", "   "},
	}

	for _, c := range cases {
		t.Run(c.method+" "+c.target+" "+c.body, func(t *testing.T) {
			t.Parallel()

			p := do(t, c.method, c.target, withBody("application/json", []byte(c.body)))
			require.Equal(t, http.StatusBadRequest, p.Nestjs.Status)
			require.Equal(t, http.StatusBadRequest, p.Go.Status)

			goBody, _ := p.Go.JSON(t).(map[string]any)
			nestBody, _ := p.Nestjs.JSON(t).(map[string]any)
			require.Equal(t, nestBody["code"], goBody["code"])
			require.Nil(t, nestBody["data"])
			require.Nil(t, goBody["data"])
		})
	}
}

func TestOversizedBody(t *testing.T) {
	t.Parallel()

	body := append(append([]byte("["), bytes.Repeat([]byte("1,"), 140000)...), []byte("1]")...)
	p := do(t, http.MethodPost, "/api/v1/nope", withBody("application/json", body))
	require.Equal(t, http.StatusRequestEntityTooLarge, p.Nestjs.Status)
	require.Equal(t, http.StatusRequestEntityTooLarge, p.Go.Status)
	require.Equal(t, p.Nestjs.JSON(t), p.Go.JSON(t))
}

func TestPercentEncodedRouteSegment(t *testing.T) {
	t.Parallel()

	requireSame(t, http.MethodGet, "/api/%68ealth")
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}
