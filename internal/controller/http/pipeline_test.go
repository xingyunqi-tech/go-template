package http_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

func TestErrorEnvelopeCarriesCodeKeyAndRequestID(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	r := h.do(http.MethodGet, "/api/v1/nope", "", "X-Request-Id", "req-1", "Accept-Language", "zh-CN")
	assert.Equal(t, http.StatusNotFound, r.Code)
	assert.Equal(t, "req-1", r.Header().Get("X-Request-Id"))
	assert.JSONEq(t, `{"code":40400,"message":"资源不存在","errorKey":"common.notFound","data":null,"requestId":"req-1"}`, r.Body.String())

	generated := h.do(http.MethodGet, "/api/v1/nope", "", "X-Request-Id", "bad id!")
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7`), generated.Header().Get("X-Request-Id"))
	assert.Equal(t, generated.Header().Get("X-Request-Id"), generated.body["requestId"])
}

func TestRoutingIsCaseAndSlashSensitiveWithMethodNotAllowed(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/api/v1/public", "").Code)
	assert.Equal(t, http.StatusNotFound, h.do(http.MethodGet, "/api/v1/public/", "").Code)
	assert.Equal(t, http.StatusNotFound, h.do(http.MethodGet, "/API/v1/public", "").Code)

	r := h.do(http.MethodPost, "/api/v1/public", "")
	assert.Equal(t, http.StatusMethodNotAllowed, r.Code)
	assert.Equal(t, "common.methodNotAllowed", r.key())
	assert.Equal(t, "GET", r.Header().Get("Allow"))

	r = h.do(http.MethodPut, "/api/v1/items/019a8f3c-6b1e-7d2a-9c4f-1e2d3c4b5a69", "")
	assert.Equal(t, http.StatusMethodNotAllowed, r.Code)
	assert.Equal(t, "GET, DELETE", r.Header().Get("Allow"))
}

func TestAuthentication(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)
	valid := h.token([]string{"user"}, true, time.Now())

	missing := h.do(http.MethodGet, "/api/v1/private", "")
	assert.Equal(t, http.StatusUnauthorized, missing.Code)
	assert.Equal(t, "common.unauthorized", missing.key())
	assert.Equal(t, "Bearer", missing.Header().Get("WWW-Authenticate"))

	invalid := h.do(http.MethodGet, "/api/v1/private", "", "Authorization", _bearer+"x.y.z")
	assert.Equal(t, http.StatusUnauthorized, invalid.Code)
	assert.Equal(t, `Bearer error="invalid_token"`, invalid.Header().Get("WWW-Authenticate"))

	ok := h.do(http.MethodGet, "/api/v1/private", "", "Authorization", _bearer+valid)
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	assert.NotNil(t, data(t, ok)["sub"])

	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/api/v1/public", "", "Authorization", _bearer+"garbage").Code)

	anon := h.do(http.MethodGet, "/api/v1/optional", "", "Authorization", _bearer+"garbage")
	assert.Equal(t, http.StatusOK, anon.Code)
	assert.Nil(t, data(t, anon)["sub"])
}

func TestExpiredAndRevokedTokens(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	iat, exp := time.Now().Add(-time.Hour), time.Now().Add(-30*time.Minute)
	c := h.signer
	expired, err := c.Sign(claimsAt(iat, exp))
	require.NoError(t, err)

	r := h.do(http.MethodGet, "/api/v1/private", "", "Authorization", _bearer+expired)
	assert.Equal(t, http.StatusUnauthorized, r.Code)
	assert.Equal(t, "auth.tokenExpired", r.key())
	assert.Equal(t, `Bearer error="invalid_token"`, r.Header().Get("WWW-Authenticate"))

	claims := claimsAt(time.Now(), time.Now().Add(time.Minute))
	token, err := c.Sign(claims)
	require.NoError(t, err)
	require.NoError(t, h.revoked.Revoke(t.Context(), claims.SessionID, time.Minute))

	r = h.do(http.MethodGet, "/api/v1/private", "", "Authorization", _bearer+token)
	assert.Equal(t, http.StatusUnauthorized, r.Code)
	assert.Equal(t, "common.unauthorized", r.key())
}

func TestPermissionsReauthAndVerifiedEmail(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)
	admin := h.token([]string{"admin"}, false, time.Now().Add(-time.Hour))
	user := h.token([]string{"user"}, true, time.Now())

	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/api/v1/admin", "", "Authorization", _bearer+admin).Code)
	assert.Equal(t, "common.forbidden", h.do(http.MethodGet, "/api/v1/admin", "", "Authorization", _bearer+user).key())

	assert.Equal(t, http.StatusOK, h.do(http.MethodPost, "/api/v1/reauth", "", "Authorization", _bearer+user).Code)

	stale := h.do(http.MethodPost, "/api/v1/reauth", "", "Authorization", _bearer+admin)
	assert.Equal(t, http.StatusForbidden, stale.Code)
	assert.Equal(t, "auth.reauthRequired", stale.key())

	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/api/v1/verified", "", "Authorization", _bearer+user).Code)
	assert.Equal(t, "auth.emailNotVerified", h.do(http.MethodGet, "/api/v1/verified", "", "Authorization", _bearer+admin).key())
}

func TestBodyLimitMediaTypeAndSyntax(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	big := h.do(http.MethodPost, "/api/v1/items", `{"title":"`+strings.Repeat("a", 2000)+`"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, big.Code)
	assert.Equal(t, "common.payloadTooLarge", big.key())

	text := h.do(http.MethodPost, "/api/v1/items", `title=a`, "Content-Type", "text/plain")
	assert.Equal(t, http.StatusUnsupportedMediaType, text.Code)
	assert.Equal(t, "common.unsupportedMediaType", text.key())

	broken := h.do(http.MethodPost, "/api/v1/items", `{"title":`)
	assert.Equal(t, http.StatusBadRequest, broken.Code)
	assert.Equal(t, "common.malformedJson", broken.key())

	charset := h.do(http.MethodPost, "/api/v1/items", `{"title":"ab","email":"a@example.com"}`, "Content-Type", "application/json; charset=utf-8")
	assert.Equal(t, http.StatusCreated, charset.Code, charset.Body.String())
}

type fieldCase struct {
	name   string
	body   string
	field  string
	rule   string
	params map[string]any
}

func fieldCases() []fieldCase {
	valid := `"title":"ab","email":"a@example.com"`

	return []fieldCase{
		{"required", `{"email":"a@example.com"}`, "title", entity.RuleRequired, nil},
		{"type", `{"title":1,"email":"a@example.com"}`, "title", entity.RuleType, map[string]any{"type": "string"}},
		{"email", `{"title":"ab","email":"nope"}`, "email", entity.RuleEmail, nil},
		{"url", `{` + valid + `,"site":"not a url"}`, "site", entity.RuleURL, nil},
		{"uuid", `{` + valid + `,"ref":"123"}`, "ref", entity.RuleUUID, nil},
		{"format", `{` + valid + `,"day":"17/11/2026"}`, "day", entity.RuleFormat, map[string]any{"format": "date"}},
		{"min", `{` + valid + `,"count":0}`, "count", entity.RuleMin, map[string]any{"min": 1.0}},
		{"max", `{` + valid + `,"count":11}`, "count", entity.RuleMax, map[string]any{"max": 10.0}},
		{"exclusive min", `{` + valid + `,"ratio":0}`, "ratio", entity.RuleMin, map[string]any{"min": 0.0, "exclusive": true}},
		{"exclusive max", `{` + valid + `,"ratio":1}`, "ratio", entity.RuleMax, map[string]any{"max": 1.0, "exclusive": true}},
		{"minLength", `{"title":"a","email":"a@example.com"}`, "title", entity.RuleMinLength, map[string]any{"minLength": 2.0}},
		{"maxLength", `{"title":"abcdef","email":"a@example.com"}`, "title", entity.RuleMaxLength, map[string]any{"maxLength": 5.0}},
		{"pattern", `{` + valid + `,"code":"abc"}`, "code", entity.RulePattern, nil},
		{"enum", `{` + valid + `,"color":"blue"}`, "color", entity.RuleEnum, map[string]any{"values": []any{"red", "green"}}},
		{"const", `{` + valid + `,"kind":"other"}`, "kind", entity.RuleEnum, map[string]any{"values": []any{"item"}}},
		{"minItems", `{` + valid + `,"tags":[]}`, "tags", entity.RuleMinItems, map[string]any{"minItems": 1.0}},
		{"maxItems", `{` + valid + `,"tags":[{"name":"aa"},{"name":"bb"},{"name":"cc"}]}`, "tags", entity.RuleMaxItems, map[string]any{"maxItems": 2.0}},
		{"nested required", `{` + valid + `,"tags":[{}]}`, "tags[0].name", entity.RuleRequired, nil},
		{"nested minLength", `{` + valid + `,"tags":[{"name":"aa"},{"name":"b"}]}`, "tags[1].name", entity.RuleMinLength, map[string]any{"minLength": 2.0}},
		{"null not allowed", `{"title":null,"email":"a@example.com"}`, "title", entity.RuleType, map[string]any{"type": "string"}},
	}
}

func TestValidationMapsEveryKeywordOnBothDocuments(t *testing.T) {
	t.Parallel()

	for _, inline := range []bool{false, true} {
		h := newHarness(t, inline, nil)

		for _, c := range fieldCases() {
			r := h.do(http.MethodPost, "/api/v1/items", c.body)
			require.Equal(t, http.StatusUnprocessableEntity, r.Code, "%s inline=%v: %s", c.name, inline, r.Body.String())
			assert.Equal(t, "common.validationFailed", r.key())
			assert.InDelta(t, 42200, r.body["code"], 0)

			errs := fieldErrors(t, r)
			require.NotEmpty(t, errs, c.name)

			first := errs[0]
			assert.Equal(t, c.field, first["field"], "%s inline=%v", c.name, inline)
			assert.Equal(t, c.rule, first["rule"], "%s inline=%v", c.name, inline)
			assert.NotEmpty(t, first["message"], c.name)

			if c.params != nil {
				assert.Equal(t, c.params, first["params"], "%s inline=%v", c.name, inline)
			}
		}
	}
}

func TestValidationReportsEveryFailedField(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	r := h.do(http.MethodPost, "/api/v1/items", `{"title":"a","email":"x","count":99}`, "Accept-Language", "zh")
	require.Equal(t, http.StatusUnprocessableEntity, r.Code)

	var fields []string
	for _, e := range fieldErrors(t, r) {
		fields = append(fields, e["field"].(string)) //nolint:forcetypeassert,errcheck // checked by the schema
	}

	assert.Equal(t, []string{"count", "email", "title"}, fields)
	assert.Equal(t, "数据验证失败", r.body["message"])
}

func TestLengthCountsCodePoints(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	for _, title := range []string{"你好", "😀😀", "😀😀😀😀😀", "é́x"} {
		r := h.do(http.MethodPost, "/api/v1/items", `{"title":"`+title+`","email":"a@example.com"}`)
		assert.Equal(t, http.StatusCreated, r.Code, "%q: %s", title, r.Body.String())
	}

	r := h.do(http.MethodPost, "/api/v1/items", `{"title":"😀","email":"a@example.com"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, r.Code)

	r = h.do(http.MethodPost, "/api/v1/items", `{"title":"😀😀😀😀😀😀","email":"a@example.com"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, r.Code)
}

func TestParametersAreValidated(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)
	id := "019a8f3c-6b1e-7d2a-9c4f-1e2d3c4b5a69"

	assert.Equal(t, http.StatusOK, h.do(http.MethodGet, "/api/v1/items/"+id+"?limit=5", "").Code)

	for target, want := range map[string][2]string{
		"/api/v1/items/nope":               {"path.id", entity.RuleUUID},
		"/api/v1/items/" + id + "?limit=0": {"query.limit", entity.RuleMin},
		"/api/v1/items/" + id + "?limit=x": {"query.limit", entity.RuleType},
	} {
		r := h.do(http.MethodGet, target, "")
		require.Equal(t, http.StatusUnprocessableEntity, r.Code, target)

		first := fieldErrors(t, r)[0]
		assert.Equal(t, want[0], first["field"], target)
		assert.Equal(t, want[1], first["rule"], target)
	}
}

func TestRateLimitHeadersAndRejection(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, func(o *httpapi.Options) { o.RateLimits = true })
	body := `{"title":"ab","email":"a@example.com"}`

	first := h.do(http.MethodPost, "/api/v1/items", body, "X-Forwarded-For", "203.0.113.9")
	require.Equal(t, http.StatusCreated, first.Code)
	assert.Equal(t, "3", first.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "2", first.Header().Get("X-RateLimit-Remaining"))
	assert.Equal(t, "360", first.Header().Get("X-RateLimit-Reset"))

	h.do(http.MethodPost, "/api/v1/items", body, "X-Forwarded-For", "203.0.113.9")
	h.do(http.MethodPost, "/api/v1/items", body, "X-Forwarded-For", "203.0.113.9")

	rejected := h.do(http.MethodPost, "/api/v1/items", body, "X-Forwarded-For", "203.0.113.9")
	assert.Equal(t, http.StatusTooManyRequests, rejected.Code)
	assert.Equal(t, "common.tooManyRequests", rejected.key())
	assert.Equal(t, "360", rejected.Header().Get("Retry-After"))
	assert.Empty(t, rejected.Header().Get("X-RateLimit-Limit"))

	public := h.do(http.MethodGet, "/api/v1/public", "", "X-Forwarded-For", "203.0.113.10")
	assert.Equal(t, "100", public.Header().Get("X-RateLimit-Limit"))
	assert.Equal(t, "99", public.Header().Get("X-RateLimit-Remaining"))

	off := newHarness(t, true, nil)
	assert.Empty(t, off.do(http.MethodGet, "/api/v1/public", "").Header().Get("X-RateLimit-Limit"))
}

func TestIdempotencyKeyReplaysAndRejects(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)
	body := `{"title":"ab","email":"a@example.com"}`

	first := h.do(http.MethodPost, "/api/v1/items", body, "Idempotency-Key", "k1")
	require.Equal(t, http.StatusCreated, first.Code)

	again := h.do(http.MethodPost, "/api/v1/items", body, "Idempotency-Key", "k1")
	assert.Equal(t, http.StatusCreated, again.Code)
	assert.Equal(t, first.Body.String(), again.Body.String())

	other := h.do(http.MethodPost, "/api/v1/items", `{"title":"abc","email":"a@example.com"}`, "Idempotency-Key", "k1")
	assert.Equal(t, http.StatusUnprocessableEntity, other.Code)
	assert.Equal(t, "common.idempotencyKeyMismatch", other.key())

	invalid := h.do(http.MethodPost, "/api/v1/items", `{"title":"a","email":"a@example.com"}`, "Idempotency-Key", "k2")
	require.Equal(t, http.StatusUnprocessableEntity, invalid.Code)

	fresh := h.do(http.MethodPost, "/api/v1/items", body, "Idempotency-Key", "k3")
	assert.NotEqual(t, first.Body.String(), fresh.Body.String())
}

func TestInternalErrorsHideTheCause(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	r := h.do(http.MethodDelete, "/api/v1/items/019a8f3c-6b1e-7d2a-9c4f-1e2d3c4b5a69", "")
	assert.Equal(t, http.StatusInternalServerError, r.Code)
	assert.Equal(t, "common.internalError", r.key())
	assert.NotContains(t, r.Body.String(), "exploded")

	p := h.do(http.MethodGet, "/api/v1/boom", "")
	assert.Equal(t, http.StatusInternalServerError, p.Code)
	assert.Equal(t, "common.internalError", p.key())
}

func TestCORSAndSecurityHeaders(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	pre := h.do(http.MethodOptions, "/api/v1/items", "", "Origin", "https://app.example.com", "Access-Control-Request-Method", "POST")
	assert.Equal(t, http.StatusNoContent, pre.Code)
	assert.Equal(t, "https://app.example.com", pre.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, pre.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key")
	assert.Empty(t, pre.Header().Get("Access-Control-Allow-Credentials"))

	other := h.do(http.MethodGet, "/api/v1/public", "", "Origin", "https://evil.example.com")
	assert.Empty(t, other.Header().Get("Access-Control-Allow-Origin"))

	allowed := h.do(http.MethodGet, "/api/v1/public", "", "Origin", "https://app.example.com")
	assert.Contains(t, allowed.Header().Get("Access-Control-Expose-Headers"), "X-RateLimit-Remaining")
	assert.Equal(t, "nosniff", allowed.Header().Get("X-Content-Type-Options"))

	anyOrigin := newHarness(t, true, func(o *httpapi.Options) { o.AnyOrigin = true })
	assert.Equal(t, "https://x.example.com", anyOrigin.do(http.MethodGet, "/api/v1/public", "", "Origin", "https://x.example.com").Header().Get("Access-Control-Allow-Origin"))
}

func TestOperationsRoutes(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true, nil)

	live := h.do(http.MethodGet, "/health/live", "")
	assert.Equal(t, http.StatusOK, live.Code)
	assert.Equal(t, "ok", live.body["status"])

	ready := h.do(http.MethodGet, "/health/ready", "")
	assert.Equal(t, http.StatusOK, ready.Code)
	assert.Equal(t, map[string]any{"database": "ok"}, ready.body["checks"])

	down := newHarness(t, true, func(o *httpapi.Options) { o.Readiness = readyStub{ok: false} })
	notReady := down.do(http.MethodGet, "/health/ready", "")
	assert.Equal(t, http.StatusServiceUnavailable, notReady.Code)
	assert.NotEmpty(t, notReady.Header().Get("Retry-After"))

	assert.Equal(t, http.StatusUnauthorized, h.do(http.MethodGet, "/metrics", "").Code)
	assert.Equal(t, "metrics", h.do(http.MethodGet, "/metrics", "", "Authorization", _bearer+_metricsToken).Body.String())

	assert.JSONEq(t, `{"openapi":"3.1.0"}`, h.do(http.MethodGet, "/openapi.json", "").Body.String())
	assert.Contains(t, h.do(http.MethodGet, "/docs", "").Body.String(), "@scalar/api-reference@1.80.0")

	hidden := newHarness(t, true, func(o *httpapi.Options) { o.DocsEnabled = false })
	assert.Equal(t, http.StatusNotFound, hidden.do(http.MethodGet, "/openapi.json", "").Code)
}

func TestRouterRefusesOperationsWithoutPolicyOrHandler(t *testing.T) {
	t.Parallel()

	policies := testPolicies()
	delete(policies, "getBoom")

	_, err := httpapi.NewRouter(t.Context(), &httpapi.Options{ValidationDocument: specJSON(t, true), Policies: policies, Register: register})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "getBoom")

	_, err = httpapi.NewRouter(t.Context(), &httpapi.Options{ValidationDocument: specJSON(t, true), Policies: testPolicies(), Register: nil})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no handler")

	withUseCaseRule := testPolicies()
	withUseCaseRule["getPublic"] = httpapi.Policy{Auth: httpapi.AuthPublic, RateLimits: []string{"auth-login-account"}}

	_, err = httpapi.NewRouter(t.Context(), &httpapi.Options{ValidationDocument: specJSON(t, true), Policies: withUseCaseRule, Register: register})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth-login-account")
}

func TestUnreachableKeySetIsUnavailable(t *testing.T) {
	t.Parallel()

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(jwks.Close)

	remote, err := jwtx.NewRemoteKeys(t.Context(), jwks.URL)
	require.NoError(t, err)

	h := newHarness(t, true, func(o *httpapi.Options) { o.Verifier = jwtx.NewVerifier(remote, "iss", "aud") })

	r := h.do(http.MethodGet, "/api/v1/private", "", "Authorization", _bearer+h.token([]string{"user"}, true, time.Now()))
	assert.Equal(t, http.StatusServiceUnavailable, r.Code)
	assert.Equal(t, "common.serviceUnavailable", r.key())
}
