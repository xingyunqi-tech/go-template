//go:build contract

package contract_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	v1 "github.com/xingyunqi-tech/go-template/internal/controller/http/v1"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
	"github.com/xingyunqi-tech/go-template/openapi"
	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

const root = "../.."

// operationsRoutes are registered outside the document (platform conventions §1).
var operationsRoutes = []string{ //nolint:gochecknoglobals // whitelist
	"GET /health/live", "GET /health/ready", "GET /metrics", "GET /openapi.json", "GET /docs", "GET /.well-known/jwks.json",
}

var _operationID = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

type ready struct{}

func (ready) Ready(context.Context) health.Report {
	return health.Report{Ready: true, Checks: map[string]string{}}
}

func loadDocument(t *testing.T) *openapi3.T {
	t.Helper()

	doc, err := openapi3.NewLoader().LoadFromData(openapi.Document)
	require.NoError(t, err)
	require.NoError(t, doc.Validate(t.Context()))

	return doc
}

func newRouter(t *testing.T) *httpapi.Router {
	t.Helper()

	translator, err := i18n.New("zh")
	require.NoError(t, err)

	keys, err := jwtx.NewStaticKeys()
	require.NoError(t, err)

	r, err := httpapi.NewRouter(t.Context(), &httpapi.Options{
		Log: slog.New(slog.DiscardHandler), Translator: translator, ServiceName: "contract",
		ValidationDocument: httpapi.ValidationDocument, Document: openapi.Document, DocsEnabled: true,
		Policies: httpapi.Policies(), Register: v1.Register(v1.New(&v1.Deps{Notes: nil, Log: slog.New(slog.DiscardHandler)})),
		Verifier: jwtx.NewVerifier(keys, "iss", "aud"), Limiter: ratelimit.New(nil), Readiness: ready{},
		Metrics: http.NotFoundHandler(),
	})
	require.NoError(t, err)

	return r
}

func TestBundleIsUpToDate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tool := filepath.Join(root, "bin", "openapigen")

	if _, err := os.Stat(tool); err != nil {
		t.Fatalf("%s is missing; run make generate", tool)
	}

	config := filepath.Join(dir, "oapi-codegen.yaml")
	raw, err := os.ReadFile(filepath.Join(root, "openapi", "oapi-codegen.yaml"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(config, bytes.ReplaceAll(raw, []byte("output: internal/controller/http/gen/gen.go"),
		[]byte("output: "+filepath.Join(dir, "gen.go"))), 0o600))

	cmd := exec.CommandContext(t.Context(), tool, "-src", filepath.Join(root, "openapi", "src"),
		"-out", filepath.Join(dir, "openapi.json"), "-validation", filepath.Join(dir, "validation.json"), "-config", config)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	for generated, committed := range map[string]string{
		"openapi.json":    "openapi/openapi.json",
		"validation.json": "internal/controller/http/validation.json",
	} {
		want, err := os.ReadFile(filepath.Join(root, committed))
		require.NoError(t, err)

		got, err := os.ReadFile(filepath.Join(dir, generated))
		require.NoError(t, err)
		assert.True(t, bytes.Equal(want, got), "%s differs from a fresh bundle; run make openapi", committed)
	}
}

func TestOpenAPIJSONServesTheCommittedBytes(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/openapi.json", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code)

	committed, err := os.ReadFile(filepath.Join(root, "openapi", "openapi.json"))
	require.NoError(t, err)

	body, err := io.ReadAll(rec.Body)
	require.NoError(t, err)
	assert.True(t, bytes.Equal(committed, body))
}

func TestRoutesAndDocumentMatch(t *testing.T) {
	t.Parallel()

	doc := loadDocument(t)

	documented := map[string]bool{}

	for path, item := range doc.Paths.Map() {
		for method := range item.Operations() {
			documented[method+" "+path] = true
		}
	}

	registered := map[string]bool{}

	err := chi.Walk(newRouter(t).Routes(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[method+" "+route] = true

		return nil
	})
	require.NoError(t, err)

	for route := range registered {
		if !slices.Contains(operationsRoutes, route) {
			assert.True(t, documented[route], "%s is registered but not documented", route)
		}
	}

	for route := range documented {
		assert.True(t, registered[route], "%s is documented but not registered", route)
		assert.True(t, strings.HasPrefix(strings.SplitN(route, " ", 2)[1], httpapi.APIPrefix+"/"), "%s is outside %s", route, httpapi.APIPrefix)
	}
}

func TestOperationsFollowTheConventions(t *testing.T) {
	t.Parallel()

	doc := loadDocument(t)
	policies := httpapi.Policies()
	seen := map[string]bool{}

	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			id := op.OperationID
			where := method + " " + path

			assert.Regexp(t, _operationID, id, where)
			assert.False(t, seen[id], "%s: operationId %s is not unique", where, id)
			seen[id] = true

			if policy, ok := policies[id]; assert.True(t, ok, "%s has no policy", id) {
				assert.Equal(t, policy.Auth != httpapi.AuthPublic, declaresBearer(op), "%s: bearerAuth must match the policy", id)
			}

			checkErrorResponses(t, id, op)

			if strings.HasPrefix(path, "/api/") {
				for _, code := range []string{"429", "503"} {
					assert.NotNil(t, op.Responses.Value(code), "%s does not declare %s", id, code)
				}
			}
		}
	}

	for id := range policies {
		assert.True(t, seen[id], "policy %s has no operation", id)
	}
}

func declaresBearer(op *openapi3.Operation) bool {
	return op.Security != nil && slices.ContainsFunc(*op.Security, func(r openapi3.SecurityRequirement) bool {
		_, ok := r["bearerAuth"]

		return ok
	})
}

// checkErrorResponses requires 422 to use ValidationErrorResponse and other errors ErrorResponse.
func checkErrorResponses(t *testing.T, id string, op *openapi3.Operation) {
	t.Helper()

	for code, resp := range op.Responses.Map() {
		if strings.HasPrefix(code, "2") || resp.Value == nil {
			continue
		}

		media := resp.Value.Content.Get("application/json")
		require.NotNil(t, media, "%s %s has no JSON schema", id, code)

		want := "#/components/schemas/ErrorResponse"
		if code == "422" {
			want = "#/components/schemas/ValidationErrorResponse"
		}

		assert.Equal(t, want, media.Schema.Ref, "%s %s", id, code)
	}
}

func TestErrorSchemasArePublished(t *testing.T) {
	t.Parallel()

	doc := loadDocument(t)

	for _, name := range []string{"ErrorResponse", "ValidationErrorResponse"} {
		assert.NotNil(t, doc.Components.Schemas[name], name)
	}

	assert.NotNil(t, doc.Components.SecuritySchemes["bearerAuth"])
	assert.Equal(t, "3.1.0", doc.OpenAPI)
}

// identityOperation is an operation of platform conventions §11 with its request fields.
type identityOperation struct {
	method, path string
	request      []string
	status       string
}

// identityOperations are the §11 operations implemented so far.
func identityOperations() map[string]identityOperation {
	return map[string]identityOperation{
		"registerWithEmail":       {"POST", "/api/v1/auth/email/register", []string{"email", "password", "displayName"}, "201"},
		"loginWithEmail":          {"POST", "/api/v1/auth/email/login", []string{"email", "password"}, "200"},
		"verifyEmail":             {"POST", "/api/v1/auth/email/verify", []string{"token"}, "200"},
		"resendVerificationEmail": {"POST", "/api/v1/auth/email/verify/resend", nil, "202"},
		"forgotPassword":          {"POST", "/api/v1/auth/password/forgot", []string{"email"}, "202"},
		"resetPassword":           {"POST", "/api/v1/auth/password/reset", []string{"token", "password"}, "200"},
		"sendSmsCode":             {"POST", "/api/v1/auth/sms/send", []string{"phone", "scene"}, "202"},
		"loginWithSms":            {"POST", "/api/v1/auth/sms/login", []string{"phone", "code"}, "200"},
		"listAuthProviders":       {"GET", "/api/v1/auth/providers", nil, "200"},
		"loginWithProvider":       {"POST", "/api/v1/auth/providers/{provider}/login", []string{"code"}, "200"},
		"refreshToken":            {"POST", "/api/v1/auth/refresh", []string{"refreshToken"}, "200"},
		"logout":                  {"POST", "/api/v1/auth/logout", nil, "200"},
		"getMe":                   {"GET", "/api/v1/me", nil, "200"},
		"updateMe":                {"PATCH", "/api/v1/me", []string{"displayName", "avatarFileId"}, "200"},
		"deleteMe":                {"DELETE", "/api/v1/me", nil, "202"},
		"changePassword":          {"PUT", "/api/v1/me/password", []string{"currentPassword", "newPassword"}, "200"},
		"bindPhone":               {"PUT", "/api/v1/me/phone", []string{"phone", "code"}, "200"},
		"bindPhoneWithWechat":     {"PUT", "/api/v1/me/phone/wechat", []string{"code"}, "200"},
		"listMySessions":          {"GET", "/api/v1/me/sessions", nil, "200"},
		"revokeMySession":         {"DELETE", "/api/v1/me/sessions/{id}", nil, "200"},
		"revokeOtherSessions":     {"POST", "/api/v1/me/sessions/revoke-others", nil, "200"},
		"listUsers":               {"GET", "/api/v1/users", nil, "200"},
		"createUser":              {"POST", "/api/v1/users", []string{"email", "phone", "password", "displayName"}, "201"},
		"getUser":                 {"GET", "/api/v1/users/{id}", nil, "200"},
		"updateUser":              {"PATCH", "/api/v1/users/{id}", []string{"displayName", "status"}, "200"},
		"deleteUser":              {"DELETE", "/api/v1/users/{id}", nil, "202"},
		"setUserRoles":            {"PUT", "/api/v1/users/{id}/roles", []string{"roles"}, "200"},
		"listRoles":               {"GET", "/api/v1/roles", nil, "200"},
		"listPermissions":         {"GET", "/api/v1/permissions", nil, "200"},
		"uploadFile":              {"POST", "/api/v1/files", []string{"file", "purpose"}, "201"},
		"getFile":                 {"GET", "/api/v1/files/{id}", nil, "200"},
		"getFileContent":          {"GET", "/api/v1/files/{id}/content", nil, "200"},
	}
}

func propertyNames(s *openapi3.SchemaRef) []string {
	names := make([]string, 0, len(s.Value.Properties))
	for name := range s.Value.Properties {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

func sorted(names []string) []string {
	out := slices.Clone(names)
	slices.Sort(out)

	return out
}

func TestIdentityOperationsFollowTheConventions(t *testing.T) {
	t.Parallel()

	doc := loadDocument(t)

	for id, want := range identityOperations() {
		item := doc.Paths.Find(want.path)
		if !assert.NotNil(t, item, "%s: %s is not documented", id, want.path) {
			continue
		}

		op := item.GetOperation(want.method)
		if !assert.NotNil(t, op, "%s: %s %s is not documented", id, want.method, want.path) {
			continue
		}

		assert.Equal(t, id, op.OperationID, "%s %s", want.method, want.path)
		assert.NotNil(t, op.Responses.Value(want.status), "%s answers %s", id, want.status)

		for code := range op.Responses.Map() {
			if strings.HasPrefix(code, "2") {
				assert.Equal(t, want.status, code, "%s has one success status", id)
			}
		}

		checkRequestBody(t, id, op, want.request)
	}
}

// checkRequestBody compares the fields of the JSON or multipart body of op with want; nil wants
// no body.
func checkRequestBody(t *testing.T, id string, op *openapi3.Operation, want []string) {
	t.Helper()

	if want == nil {
		assert.Nil(t, op.RequestBody, "%s takes no body", id)

		return
	}

	if !assert.NotNil(t, op.RequestBody, "%s needs a body", id) {
		return
	}

	media := op.RequestBody.Value.Content.Get("application/json")
	if media == nil {
		media = op.RequestBody.Value.Content.Get("multipart/form-data")
	}

	if assert.NotNil(t, media, "%s takes JSON or a multipart form", id) {
		assert.Equal(t, sorted(want), propertyNames(media.Schema), id)
	}
}

func TestIdentityObjectsHaveTheConventionFields(t *testing.T) {
	t.Parallel()

	schemas := loadDocument(t).Components.Schemas

	for name, fields := range map[string][]string{
		"User":          {"id", "email", "emailVerified", "phone", "displayName", "avatarUrl", "status", "roles", "hasPassword", "createdAt", "updatedAt"},
		"Session":       {"id", "userAgent", "ip", "createdAt", "lastActiveAt", "current"},
		"Role":          {"code", "name", "permissions"},
		"Permission":    {"code"},
		"LoginResponse": {"accessToken", "refreshToken", "expiresIn", "tokenType", "user"},
		"AuthProvider":  {"id"},
		"File":          {"id", "name", "size", "contentType", "purpose", "url", "createdAt"},
	} {
		if assert.Contains(t, schemas, name) {
			assert.Equal(t, sorted(fields), propertyNames(schemas[name]), name)
			assert.Equal(t, sorted(fields), sorted(schemas[name].Value.Required), "%s fields are all required", name)
		}
	}
}

func TestSmsScenesAndProvidersFollowTheConventions(t *testing.T) {
	t.Parallel()

	doc := loadDocument(t)

	scene := doc.Components.Schemas["SendSmsCodeInput"].Value.Properties["scene"].Value
	assert.Equal(t, []any{"login", "bind_phone"}, scene.Enum)

	provider := doc.Paths.Find("/api/v1/auth/providers/{provider}/login").Parameters[0].Value.Schema.Value
	assert.True(t, provider.Type.Is("string"))
	assert.Empty(t, provider.Enum, "provider is a plain string")
}

func TestFileContentIsServedUnenveloped(t *testing.T) {
	t.Parallel()

	op := loadDocument(t).Paths.Find("/api/v1/files/{id}/content").Get

	assert.Empty(t, op.Security, "getFileContent is authorized by its signature")

	content := op.Responses.Value("200").Value.Content
	for _, media := range []string{"image/jpeg", "image/png", "image/webp", "application/pdf"} {
		assert.NotNil(t, content.Get(media), media)
	}

	assert.Nil(t, content.Get("application/json"))
}
