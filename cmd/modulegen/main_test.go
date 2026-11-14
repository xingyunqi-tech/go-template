package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func copyFile(t *testing.T, from, to string) {
	t.Helper()

	raw, err := os.ReadFile(from)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(to), 0o755))
	require.NoError(t, os.WriteFile(to, raw, 0o600)) //nolint:gosec // test fixture paths
}

func fixtureRoot(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	for _, f := range []string{"openapi/nestjs-baseline.json", "openapi/scope.yaml", "internal/repo/contracts.go"} {
		copyFile(t, filepath.Join("..", "..", f), filepath.Join(root, f))
	}

	return root
}

func TestScaffoldsAModuleForATag(t *testing.T) {
	t.Parallel()

	root := fixtureRoot(t)

	var out bytes.Buffer
	require.NoError(t, run([]string{"-name", "orders", "-tag", "Orders"}, root, &out))

	handlers, err := os.ReadFile(filepath.Join(root, "internal", "controller", "http", "v1", "orders.go"))
	require.NoError(t, err)
	assert.Contains(t, string(handlers), "gen.CreateOrderRequestObject")
	assert.Contains(t, string(handlers), "httperr.Status(http.StatusNotImplemented)")

	usecase, err := os.ReadFile(filepath.Join(root, "internal", "usecase", "orders", "orders.go"))
	require.NoError(t, err)
	assert.Contains(t, string(usecase), "store repo.Orders")

	contracts, err := os.ReadFile(filepath.Join(root, "internal", "repo", "contracts.go"))
	require.NoError(t, err)
	assert.Contains(t, string(contracts), "type Orders interface{}")

	scope, err := os.ReadFile(filepath.Join(root, "openapi", "scope.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(scope), "  - createOrder\n")

	assert.Contains(t, out.String(), `"createOrder": {Auth: AuthJWT}`)
}

func TestRefusesToOverwriteAndUnknownTags(t *testing.T) {
	t.Parallel()

	root := fixtureRoot(t)
	require.NoError(t, run([]string{"-name", "orders", "-tag", "Orders"}, root, &bytes.Buffer{}))

	err := run([]string{"-name", "orders", "-tag", "Orders"}, root, &bytes.Buffer{})
	require.ErrorIs(t, err, errExists)

	require.ErrorIs(t, run([]string{"-name", "nope", "-tag", "Nope"}, root, &bytes.Buffer{}), errNoTag)
	require.ErrorIs(t, run([]string{"-name", "Bad-Name", "-tag", "Orders"}, root, &bytes.Buffer{}), errUsage)

	scope, err := os.ReadFile(filepath.Join(root, "openapi", "scope.yaml"))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(scope), "- createOrder\n"))
}
