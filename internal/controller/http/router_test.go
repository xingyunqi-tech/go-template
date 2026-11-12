package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	v1 "github.com/xingyunqi-tech/go-template/internal/controller/http/v1"
)

var testDocs = []byte(`{"openapi":"3.0.0"}`) //nolint:gochecknoglobals // shared fixture

func newRouter(t *testing.T, swagger bool) http.Handler {
	t.Helper()

	h, err := httpapi.NewRouter(v1.New("demo"), httpapi.Options{APIPrefix: "api", SwaggerEnabled: swagger, Docs: testDocs})
	require.NoError(t, err)

	return h
}

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody))

	return rec
}

func TestAppInfo(t *testing.T) {
	t.Parallel()

	rec := serve(t, newRouter(t, false), http.MethodGet, "/")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":200,"message":"success","data":{"name":"demo"}}`, rec.Body.String())
}

func TestAcceptLanguageHeaderIsAccepted(t *testing.T) {
	t.Parallel()

	h := newRouter(t, false)

	for _, values := range [][]string{{"en"}, {"zh-CN,zh;q=0.9,en;q=0.8"}, {"en", "zh"}} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		for _, v := range values {
			req.Header.Add("Accept-Language", v)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, values)
	}
}

func TestHeadOnGetRoute(t *testing.T) {
	t.Parallel()

	rec := serve(t, newRouter(t, false), http.MethodHead, "/")

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestNotFoundEnvelope(t *testing.T) {
	t.Parallel()

	h := newRouter(t, false)

	tests := []struct {
		method, target, message string
	}{
		{http.MethodGet, "/nope?x=1", "Cannot GET /nope?x=1"},
		{http.MethodPost, "/", "Cannot POST /"},
		{http.MethodGet, "/api/%68ealth", "Cannot GET /api/%68ealth"},
		{http.MethodGet, "/docs-json", "Cannot GET /docs-json"},
	}

	for _, tt := range tests {
		rec := serve(t, h, tt.method, tt.target)

		assert.Equal(t, http.StatusNotFound, rec.Code, tt.target)
		assert.Empty(t, rec.Header().Get("Allow"))
		assert.JSONEq(t, `{"code":404,"message":"`+tt.message+`","data":null}`, rec.Body.String())
	}
}

func TestDocs(t *testing.T) {
	t.Parallel()

	h := newRouter(t, true)

	rec := serve(t, h, http.MethodGet, "/docs-json")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, testDocs, rec.Body.Bytes())

	rec = serve(t, h, http.MethodGet, "/docs")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-url="/docs-json"`)
	assert.NotEmpty(t, rec.Header().Get("Content-Security-Policy"))
}
