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
