package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const _pipelineSpec = `{"paths":{
 "/api/v1/items/{id}":{"patch":{"operationId":"updateItem",
   "parameters":[{"in":"path","name":"id","schema":{"type":"number"}}],
   "security":[{"bearer":[]}],
   "requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/AuthEmailLoginDto"}}}}}},
 "/api/v1/items":{"get":{"operationId":"listItems"}},
 "/api/v1/codes/{code}":{"get":{"operationId":"getCode",
   "parameters":[{"in":"path","name":"code","schema":{"type":"string"}}]}}
}}`

func pipelinePolicies() map[string]Policy {
	return map[string]Policy{
		"updateItem": {Auth: AuthJWT},
		"listItems":  {Auth: AuthNone, Query: "AdminPaginationQueryDto"},
		"getCode":    {Auth: AuthNone},
	}
}

func requireToken(r *http.Request) (*http.Request, error) {
	if r.Header.Get("Authorization") == "" {
		return r, httperr.Status(http.StatusUnauthorized)
	}

	return r, nil
}

func echo(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	parts := []string{"body=" + string(body), "query=" + r.URL.RawQuery}
	for i, k := range chi.RouteContext(r.Context()).URLParams.Keys {
		parts = append(parts, k+"="+chi.RouteContext(r.Context()).URLParams.Values[i])
	}

	_, _ = w.Write([]byte(strings.Join(parts, " "))) //nolint:gosec // test echo handler
}

func newPipelineChain(t *testing.T) http.Handler {
	t.Helper()

	translator, err := i18n.New("en")
	require.NoError(t, err)

	validator, err := validatorx.New(openapi.Validation)
	require.NoError(t, err)

	errs := &errorWriter{translator: translator, log: slog.New(slog.DiscardHandler)}

	mux := chi.NewRouter()
	mux.Use(middleware.GetHead)
	mux.Patch("/api/v1/items/{id}", echo)
	mux.Get("/api/v1/items", echo)
	mux.Get("/api/v1/codes/{code}", echo)

	ops, err := parseOperations([]byte(_pipelineSpec))
	require.NoError(t, err)

	pipe := &pipeline{
		ops:        ops,
		policies:   pipelinePolicies(),
		guards:     map[Auth]Guard{AuthJWT: requireToken},
		permission: nil,
		validator:  validator,
		errs:       errs,
	}

	matcher, err := newPathMatcher(mux, "api")
	require.NoError(t, err)

	return requestContext(translator)(jsonBody(errs)(matcher.middleware(pipe.middleware(mux))))
}

func send(t *testing.T, h http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestPipelineOrder(t *testing.T) {
	t.Parallel()

	h := newPipelineChain(t)
	jsonType := map[string]string{"Content-Type": "application/json"}
	authed := map[string]string{"Content-Type": "application/json", "Authorization": "Bearer x"}
	validBody := `{"email":"A@Example.com","password":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","extra":1}`

	tests := []struct {
		name    string
		target  string
		body    string
		headers map[string]string
		status  int
		want    string
	}{
		{"invalid json before missing token", "/api/v1/items/1", "{bad", jsonType, 400, `"data":null`},
		{"missing token before validation", "/api/v1/items/1", "{}", jsonType, 401, `"message":"Unauthorized"`},
		{"missing token before invalid id", "/api/v1/items/abc", "{}", jsonType, 401, `"code":401`},
		{"invalid id before validation", "/api/v1/items/abc", "{}", authed, 400, `{"code":400,"message":"Bad request","data":{"id":"Invalid id"}}`},
		{"zero id", "/api/v1/items/0", validBody, authed, 400, `"id":"Invalid id"`},
		{"id above int32", "/api/v1/items/2147483648", validBody, authed, 400, `"id":"Invalid id"`},
		{
			"validation errors", "/api/v1/items/7", `{}`, authed, 422,
			`{"code":422,"message":"Validation failed","data":{"email":"email should not be empty, email must be an email",` +
				`"password":"Password must be a valid MD5 hash (32 hexadecimal characters), password should not be empty"}}`,
		},
		{"transformed body reaches handler", "/api/v1/items/7", validBody, authed, 200, `body={"email":"a@example.com","password":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} query= id=7`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := send(t, h, http.MethodPatch, tt.target, tt.body, tt.headers)
			assert.Equal(t, tt.status, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tt.want)
		})
	}
}

func TestPipelineValidatesQuery(t *testing.T) {
	t.Parallel()

	h := newPipelineChain(t)

	rec := send(t, h, http.MethodGet, "/api/v1/items?limit=5&x=1", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "body= query=page=1&limit=5", rec.Body.String())

	rec = send(t, h, http.MethodGet, "/api/v1/items?page=0", "", nil)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), `"page":"page must not be less than 1"`)
}

func TestPipelineDecodesPathParams(t *testing.T) {
	t.Parallel()

	h := newPipelineChain(t)

	rec := send(t, h, http.MethodGet, "/api/v1/codes/a%20b", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec = send(t, h, method, "/api/v1/codes/%FF", "", nil)
		assert.Equal(t, http.StatusBadRequest, rec.Code, method)
		assert.JSONEq(t, `{"code":400,"message":"Failed to decode param '%FF'","data":null}`, rec.Body.String())
	}

	rec = send(t, h, http.MethodPost, "/api/v1/codes/abc", "", nil)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestPipelineCheckReportsUnrunnableOperations(t *testing.T) {
	t.Parallel()

	validator, err := validatorx.New(openapi.Validation)
	require.NoError(t, err)

	pipe := &pipeline{
		ops:        nil,
		policies:   map[string]Policy{"jwtOp": {Auth: AuthJWT}, "rbacOp": {RBAC: true}, "queryOp": {Query: "MissingDto"}},
		guards:     nil,
		permission: nil,
		validator:  validator,
		errs:       nil,
	}

	err = pipe.check([]*operation{{id: "noPolicy"}, {id: "jwtOp"}, {id: "rbacOp"}, {id: "queryOp", bodyDTO: "AlsoMissingDto"}})
	require.Error(t, err)

	for _, want := range []string{"noPolicy has no policy", "jwtOp needs a jwt guard", "rbacOp needs the permission guard", "MissingDto", "AlsoMissingDto"} {
		assert.Contains(t, err.Error(), want)
	}
}

func TestDecodeURIComponent(t *testing.T) {
	t.Parallel()

	ok := map[string]string{"a%20b": "a b", "%E4%B8%AD": "中", "plain": "plain", "%2F": "/"}
	for in, want := range ok {
		got, err := decodeURIComponent(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got)
	}

	for _, in := range []string{"%", "%E", "%zz", "%E0%A4%A", "%FF", "%ED%A0%80"} {
		_, err := decodeURIComponent(in)
		assert.Error(t, err, in)
	}
}

func TestParseQueryStringFollowsNodeQuerystring(t *testing.T) {
	t.Parallel()

	q := parseQueryString("a=1&a=2&a=3&b=x+y&c&=e&d=%E4%B8%AD&bad=%FF%41&&")
	s, _ := validatorx.Stringify(q)
	assert.JSONEq(t, `{"a":["1","2","3"],"b":"x y","c":"","":"e","d":"中","bad":"\ufffdA"}`, s)

	assert.Equal(t, 0, parseQueryString("").Len())

	pairs := make([]string, 0, 2*_maxQueryKeys)
	for i := range 2 * _maxQueryKeys {
		pairs = append(pairs, "k"+strconv.Itoa(i)+"=v")
	}

	assert.Equal(t, _maxQueryKeys, parseQueryString(strings.Join(pairs, "&")).Len())
}

func TestEncodeQuery(t *testing.T) {
	t.Parallel()

	v, err := validatorx.ParseJSON([]byte(`{"page":1,"ids":["a","b"],"f":{"x":1},"n":null,"ok":true,"s":"a b"}`))
	require.NoError(t, err)

	assert.Equal(t, "page=1&ids=a&ids=b&f=%7B%22x%22%3A1%7D&ok=true&s=a+b", encodeQuery(v))
	assert.Empty(t, encodeQuery("x"))
}
