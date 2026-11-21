//go:build contract

package contract_test

import (
	"bufio"
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
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

const root = "../.."

// undocumentedRoutes may be registered without appearing in the baseline document.
var undocumentedRoutes = []string{"GET /docs", "GET /docs-json", "GET /metrics"} //nolint:gochecknoglobals // whitelist

type operation struct {
	method, path string
	bearer       bool
}

func readFile(t *testing.T, rel string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err)

	return data
}

func newRouter(t *testing.T) *httpapi.Router {
	t.Helper()

	translator, err := i18n.New("en")
	require.NoError(t, err)

	validator, err := validatorx.New(openapi.Validation)
	require.NoError(t, err)

	r, err := httpapi.NewRouter(v1.New(&v1.Deps{AppName: "app", Auth: nil, Users: nil, URLs: view.FileURLs{BackendDomain: "", APIPrefix: "api"}}), &httpapi.Options{
		APIPrefix:      "api",
		SwaggerEnabled: true,
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
	})
	require.NoError(t, err)

	return r
}

func loadBaseline(t *testing.T) map[string]operation {
	t.Helper()

	doc, err := openapi3.NewLoader().LoadFromData(readFile(t, "openapi/nestjs-baseline.json"))
	require.NoError(t, err)

	ops := map[string]operation{}

	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			if op.OperationID == "" {
				continue
			}

			ops[op.OperationID] = operation{method: method, path: path, bearer: hasBearer(op)}
		}
	}

	return ops
}

func hasBearer(op *openapi3.Operation) bool {
	if op.Security == nil {
		return false
	}

	for _, req := range *op.Security {
		if _, ok := req["bearer"]; ok {
			return true
		}
	}

	return false
}

func loadScope(t *testing.T) []string {
	t.Helper()

	var scope struct {
		Operations []string `yaml:"operations"`
	}
	require.NoError(t, yaml.Unmarshal(readFile(t, "openapi/scope.yaml"), &scope))
	require.NotEmpty(t, scope.Operations)

	return scope.Operations
}

func registeredRoutes(t *testing.T, routes chi.Routes) []string {
	t.Helper()

	var out []string

	err := chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+route)

		return nil
	})
	require.NoError(t, err)

	return out
}

func TestDocsJSONServesBaselineBytes(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	newRouter(t).ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/docs-json", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, bytes.Equal(readFile(t, "openapi/nestjs-baseline.json"), rec.Body.Bytes()),
		"/docs-json differs from openapi/nestjs-baseline.json")
}

func TestSourceDeclaresContractOrigin(t *testing.T) {
	t.Parallel()

	keys := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(readFile(t, "openapi/SOURCE")))

	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		require.True(t, ok, scanner.Text())

		keys[key] = value
	}

	hex := regexp.MustCompile(`^[0-9a-f]{40}$`)

	assert.Equal(t, "xingyunqi-tech/nestjs-template", keys["repository"])
	assert.NotEmpty(t, keys["branch"])
	assert.Regexp(t, hex, keys["commit"])
	assert.Regexp(t, hex, keys["validation_script"])
	assert.Equal(t, "NODE_ENV=development FILE_DRIVER=local", keys["environment"])
	assert.Len(t, keys, 5)
}

func TestEveryScopedOperationIsRegistered(t *testing.T) {
	t.Parallel()

	ops := loadBaseline(t)
	registered := registeredRoutes(t, newRouter(t).Routes())

	for _, id := range loadScope(t) {
		op, ok := ops[id]
		if !assert.True(t, ok, "%s is not in the baseline", id) {
			continue
		}

		assert.Contains(t, registered, op.method+" "+op.path, "%s has no registered handler", id)
	}
}

func TestEveryRegisteredRouteIsDocumented(t *testing.T) {
	t.Parallel()

	documented := map[string]bool{}
	for _, op := range loadBaseline(t) {
		documented[op.method+" "+op.path] = true
	}

	for _, route := range registeredRoutes(t, newRouter(t).Routes()) {
		if strings.HasPrefix(route, http.MethodHead+" ") {
			continue
		}

		assert.True(t, documented[route] || slices.Contains(undocumentedRoutes, route),
			"%s is neither documented nor whitelisted", route)
	}
}

func TestPoliciesMatchScopeAndDocumentedSecurity(t *testing.T) {
	t.Parallel()

	ops := loadBaseline(t)
	policies := httpapi.Policies()
	scope := loadScope(t)

	for _, id := range scope {
		policy, ok := policies[id]
		if !assert.True(t, ok, "%s has no policy", id) {
			continue
		}

		switch policy.Auth {
		case httpapi.AuthJWT, httpapi.AuthOptionalJWT, httpapi.AuthRefresh, httpapi.AuthMonitor:
			assert.True(t, ops[id].bearer, "%s uses %s but the document has no bearer security", id, policy.Auth)
		case httpapi.AuthNone:
			assert.False(t, ops[id].bearer, "%s is documented with bearer but has no auth", id)
		}
	}

	for id := range policies {
		if slices.Contains(httpapi.Undocumented(), id) {
			assert.NotContains(t, ops, id, "%s is documented", id)

			continue
		}

		assert.Contains(t, scope, id, "policy %s is not in scope.yaml", id)
	}
}

func TestValidationMetadataIsFullyImplemented(t *testing.T) {
	t.Parallel()

	_, err := validatorx.New(openapi.Validation)
	require.NoError(t, err)
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
