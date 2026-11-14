package http_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	v1 "github.com/xingyunqi-tech/go-template/internal/controller/http/v1"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/storage"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

func options(t *testing.T) *httpapi.Options {
	t.Helper()

	translator, err := i18n.New("en")
	require.NoError(t, err)

	validator, err := validatorx.New(openapi.Validation)
	require.NoError(t, err)

	return &httpapi.Options{
		APIPrefix:      "api",
		SwaggerEnabled: false,
		Docs:           openapi.Baseline,
		Validator:      validator,
		Translator:     translator,
		Log:            slog.New(slog.DiscardHandler),
		Production:     false,
		FrontendDomain: "",
		Guards:         httpapi.AuthGuards(rejectAll{}),
		Permission:     httpapi.RequirePermissions(noPermissions{}),
		Transactor:     nil,
		Uploads: httpapi.UploadOptions{
			Files: file.New(file.Config{APIPrefix: "api", BackendDomain: "", MaxSizes: nil}, nil,
				storage.NewLocal(t.TempDir()), nil, slog.New(slog.DiscardHandler)),
			Permissions: noPermissions{}, Presigned: false,
		},
		TrustProxy: 0,
	}
}

func newRouter(t *testing.T, swagger bool) http.Handler {
	t.Helper()

	opts := options(t)
	opts.SwaggerEnabled = swagger

	h, err := httpapi.NewRouter(v1.New(&v1.Deps{AppName: "demo", Auth: nil, Users: nil, URLs: view.FileURLs{BackendDomain: "", APIPrefix: "api"}}), opts)
	require.NoError(t, err)

	return h
}

func do(t *testing.T, h http.Handler, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	return do(t, h, method, target, http.NoBody, nil)
}

func TestAppInfo(t *testing.T) {
	t.Parallel()

	rec := serve(t, newRouter(t, false), http.MethodGet, "/")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"code":200,"message":"success","data":{"name":"demo"}}`, rec.Body.String())
}

func TestSimpleHealthCheck(t *testing.T) {
	t.Parallel()

	rec := serve(t, newRouter(t, false), http.MethodGet, "/API/Health/")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"ok"`)
	assert.Contains(t, rec.Body.String(), `"memory":{"heapUsed":`)
	assert.Contains(t, rec.Body.String(), `"node":"go1.`)
}

func TestAcceptLanguageSelectsErrorLanguage(t *testing.T) {
	t.Parallel()

	h, err := httpapi.NewRouter(panicServer{v1.New(&v1.Deps{AppName: "demo", Auth: nil, Users: nil, URLs: view.FileURLs{BackendDomain: "", APIPrefix: "api"}})}, options(t))
	require.NoError(t, err)

	tests := []struct {
		values  []string
		message string
	}{
		{nil, "Internal server error"},
		{[]string{"zh-CN,zh;q=0.9,en;q=0.8"}, "服务器内部错误"},
		{[]string{"fr", "zh;q=0.5"}, "服务器内部错误"},
		{[]string{"en;q=0.5", "zh;q=0.8"}, "服务器内部错误"},
		{[]string{"fr"}, "Internal server error"},
	}

	for _, tt := range tests {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		for _, v := range tt.values {
			req.Header.Add("Accept-Language", v)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		assert.JSONEq(t, `{"code":500,"message":"`+tt.message+`","data":null}`, rec.Body.String(), tt.values)
	}
}

func TestHeadOnGetRoute(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(newRouter(t, false))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodHead, srv.URL+"/", http.NoBody)
	require.NoError(t, err)

	res, err := srv.Client().Do(req)
	require.NoError(t, err)

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json; charset=utf-8", res.Header.Get("Content-Type"))
	assert.Empty(t, body)
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
	assert.Equal(t, openapi.Baseline, rec.Body.Bytes())

	rec = serve(t, h, http.MethodGet, "/docs")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `data-url="/docs-json"`)
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "cdn.jsdelivr.net")
}

func TestRequestID(t *testing.T) {
	t.Parallel()

	h := newRouter(t, false)

	rec := do(t, h, http.MethodGet, "/", http.NoBody, map[string]string{"X-Request-Id": "abc.1:2-3"})
	assert.Equal(t, "abc.1:2-3", rec.Header().Get("X-Request-Id"))

	for _, incoming := range []string{"", "bad id!", strings.Repeat("x", 129)} {
		rec := do(t, h, http.MethodGet, "/", http.NoBody, map[string]string{"X-Request-Id": incoming})

		id := rec.Header().Get("X-Request-Id")
		parsed, err := uuid.Parse(id)
		require.NoError(t, err, incoming)
		assert.Equal(t, id, parsed.String())
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	req.Header.Add("X-Request-Id", "a")
	req.Header.Add("X-Request-Id", "b")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.NotContains(t, []string{"a", "b"}, rec.Header().Get("X-Request-Id"))
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	rec := serve(t, newRouter(t, false), http.MethodGet, "/api/v1/nope")

	assert.Equal(t, "max-age=31536000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "SAMEORIGIN", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "cross-origin", rec.Header().Get("Cross-Origin-Resource-Policy"))
	assert.Equal(t, "0", rec.Header().Get("X-XSS-Protection"))
}

func TestCORS(t *testing.T) {
	t.Parallel()

	dev := newRouter(t, false)

	rec := do(t, dev, http.MethodOptions, "/anything", http.NoBody, map[string]string{
		"Origin": "http://a.example", "Access-Control-Request-Headers": "x-foo",
	})
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
	assert.Equal(t, "http://a.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "x-foo", rec.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "GET,HEAD,PUT,PATCH,POST,DELETE", rec.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "Origin, Access-Control-Request-Headers", rec.Header().Get("Vary"))

	opts := options(t)
	opts.Production = true
	opts.FrontendDomain = "https://a.example, https://b.example"

	prod, err := httpapi.NewRouter(v1.New(&v1.Deps{AppName: "demo", Auth: nil, Users: nil, URLs: view.FileURLs{BackendDomain: "", APIPrefix: "api"}}), opts)
	require.NoError(t, err)

	rec = do(t, prod, http.MethodOptions, "/", http.NoBody, map[string]string{"Origin": "https://b.example"})
	assert.Equal(t, "https://b.example", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Content-Type,Authorization,Accept-Language", rec.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "Origin", rec.Header().Get("Vary"))

	rec = do(t, prod, http.MethodGet, "/", http.NoBody, map[string]string{"Origin": "https://evil.example"})
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
	assert.Contains(t, rec.Header().Get("Access-Control-Expose-Headers"), "X-Request-Id")
}

func TestJSONBodyIsParsedBeforeRouting(t *testing.T) {
	t.Parallel()

	h := newRouter(t, false)
	jsonHeaders := map[string]string{"Content-Type": "application/json; charset=utf-8"}

	for _, body := range []string{"x", "{bad", "null", "   ", `"s"`, "[1,]"} {
		rec := do(t, h, http.MethodPost, "/api/v1/nope", strings.NewReader(body), jsonHeaders)

		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.Contains(t, rec.Body.String(), `"data":null`, body)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/nope", strings.NewReader(`{"a":1}`), jsonHeaders)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = do(t, h, http.MethodPost, "/api/v1/nope", strings.NewReader("x"), map[string]string{"Content-Type": "text/plain"})
	assert.Equal(t, http.StatusNotFound, rec.Code)

	big := "[" + strings.Repeat("1,", 140000) + "1]"
	rec = do(t, h, http.MethodPost, "/api/v1/nope", strings.NewReader(big), jsonHeaders)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.JSONEq(t, `{"code":413,"message":"request entity too large","data":null}`, rec.Body.String())
}

type panicServer struct {
	*v1.Server
}

func (panicServer) GetAppInfo(context.Context, gen.GetAppInfoRequestObject) (gen.GetAppInfoResponseObject, error) {
	panic("boom")
}

func TestPanicBecomesInternalServerError(t *testing.T) {
	t.Parallel()

	h, err := httpapi.NewRouter(panicServer{v1.New(&v1.Deps{AppName: "demo", Auth: nil, Users: nil, URLs: view.FileURLs{BackendDomain: "", APIPrefix: "api"}})}, options(t))
	require.NoError(t, err)

	rec := do(t, h, http.MethodGet, "/", http.NoBody, map[string]string{"Accept-Language": "zh"})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.JSONEq(t, `{"code":500,"message":"服务器内部错误","data":null}`, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("X-Request-Id"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

func gzipped(t *testing.T, body string) io.Reader {
	t.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	return &buf
}

func TestJSONBodyFollowsBodyParserEncodingRules(t *testing.T) {
	t.Parallel()

	h := newRouter(t, false)
	gz := map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"}

	rec := do(t, h, http.MethodPost, "/", strings.NewReader("{}"), map[string]string{"Content-Type": "application/json; charset=latin1"})
	assert.JSONEq(t, `{"code":415,"message":"unsupported charset \"LATIN1\"","data":null}`, rec.Body.String())

	rec = do(t, h, http.MethodPost, "/", strings.NewReader("{}"), map[string]string{"Content-Type": "application/json", "Content-Encoding": "x-custom"})
	assert.JSONEq(t, `{"code":415,"message":"unsupported content encoding \"x-custom\"","data":null}`, rec.Body.String())

	rec = do(t, h, http.MethodPost, "/", strings.NewReader("not gzip"), gz)
	assert.JSONEq(t, `{"code":400,"message":"Decompression failed","data":null}`, rec.Body.String())

	rec = do(t, h, http.MethodPost, "/api/v1/nope", gzipped(t, `{"a":1}`), gz)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	big := "[" + strings.Repeat("1,", 140000) + "1]"
	rec = do(t, h, http.MethodPost, "/", gzipped(t, big), gz)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestInvalidJSONMessageFollowsV8(t *testing.T) {
	t.Parallel()

	h := newRouter(t, false)
	jsonType := map[string]string{"Content-Type": "application/json"}

	cases := map[string]string{
		"中文":                    `Unexpected token '中', \"中文\" is not valid JSON`,
		`x"y`:                   `Unexpected token 'x', \"x\"y\" is not valid JSON`,
		"abcdefghijklmnopqrstu": `Unexpected token 'a', \"abcdefghij\"... is not valid JSON`,
	}

	for body, message := range cases {
		rec := do(t, h, http.MethodPost, "/", strings.NewReader(body), jsonType)
		assert.JSONEq(t, `{"code":400,"message":"`+message+`","data":null}`, rec.Body.String(), body)
	}

	rec := do(t, h, http.MethodPost, "/", strings.NewReader(`{"a":}`), jsonType)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NotContains(t, rec.Body.String(), "parse json")
}

type rejectAll struct{}

func (rejectAll) Authenticate(context.Context, string) (*auth.Principal, error) {
	return nil, entity.Unauthorized("")
}

func (rejectAll) AuthenticateRefresh(string) (*auth.RefreshPrincipal, error) {
	return nil, entity.Unauthorized("")
}

type noPermissions struct{}

func (noPermissions) ForRole(context.Context, int) ([]string, error) {
	return nil, nil
}
