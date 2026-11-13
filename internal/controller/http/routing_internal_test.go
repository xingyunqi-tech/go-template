package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func echoPattern(w http.ResponseWriter, r *http.Request) {
	rctx := chi.RouteContext(r.Context())
	body := rctx.RoutePattern()

	for i, key := range rctx.URLParams.Keys {
		body += " " + key + "=" + rctx.URLParams.Values[i]
	}

	_, _ = w.Write([]byte(body))
}

func notFound(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Cannot "+r.Method+" "+r.RequestURI, http.StatusNotFound)
}

func newTestMux(t *testing.T, prefix string) http.Handler {
	t.Helper()

	mux := chi.NewRouter()
	mux.Use(middleware.GetHead)
	mux.NotFound(notFound)
	mux.MethodNotAllowed(notFound)

	for _, p := range []string{"/", "/docs-json", "/api/health", "/api/v1/users", "/api/v1/users/me", "/api/v1/users/{id}"} {
		mux.Get(p, echoPattern)
	}

	mux.Patch("/api/v1/users/{id}", echoPattern)

	m, err := newPathMatcher(mux, prefix)
	require.NoError(t, err)

	return m.middleware(mux)
}

func TestPathMatching(t *testing.T) {
	t.Parallel()

	h := newTestMux(t, "api")

	tests := []struct {
		name   string
		method string
		target string
		status int
		body   string
	}{
		{"root", "", "/", 200, "/"},
		{"exact", "", "/api/health", 200, "/api/health"},
		{"trailing slash", "", "/api/health/", 200, "/api/health"},
		{"upper case static segments", "", "/API/HEALTH", 200, "/api/health"},
		{"two trailing slashes", "", "/api/health//", 404, ""},
		{"encoded static segment", "", "/api/%68ealth", 404, ""},
		{"static preferred over param", "", "/api/v1/USERS/ME", 200, "/api/v1/users/me"},
		{"param keeps case", "", "/api/v1/Users/AbC", 200, "/api/v1/users/{id} id=AbC"},
		{"param stays encoded", "", "/api/v1/users/a%2Fb", 200, "/api/v1/users/{id} id=a%2Fb"},
		{"empty param", "", "/api/v1/users//", 404, ""},
		{"unknown", "", "/nope", 404, ""},
		{"param keeps case when another method has a static sibling", http.MethodPatch, "/api/v1/users/ME", 200, "/api/v1/users/{id} id=ME"},
		{"head uses get templates", http.MethodHead, "/API/HEALTH", 200, "/api/health"},
		{"method without routes", http.MethodDelete, "/api/health", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			method := http.MethodGet
			if tt.method != "" {
				method = tt.method
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, tt.target, http.NoBody))

			assert.Equal(t, tt.status, rec.Code)

			if tt.status == http.StatusOK {
				assert.Equal(t, tt.body, rec.Body.String())
			}
		})
	}
}

func TestPathMatchingCustomPrefix(t *testing.T) {
	t.Parallel()

	h := newTestMux(t, "v")

	for target, status := range map[string]int{
		"/v/health":   200,
		"/V/v1/users": 200,
		"/api/health": 404,
		"/docs-json":  200,
		"/":           200,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody))
		assert.Equal(t, status, rec.Code, target)
	}
}
