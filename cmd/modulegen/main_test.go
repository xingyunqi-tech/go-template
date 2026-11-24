package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspace copies the files modulegen reads and edits into a temporary root.
func workspace(t *testing.T) string {
	t.Helper()

	repo, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	root := t.TempDir()

	copyFile := func(rel string) {
		data, err := os.ReadFile(filepath.Join(repo, rel))
		require.NoError(t, err)
		require.NoError(t, write(filepath.Join(root, rel), data))
	}

	for _, dir := range []string{
		filepath.Join("internal", "app"),
		filepath.Join("templates", "module"), filepath.Join("migrations", "core"), filepath.Join("internal", "entity"),
		filepath.Join("openapi", "src"), filepath.Join("openapi", "src", "paths"), filepath.Join("openapi", "src", "schemas"),
	} {
		entries, err := os.ReadDir(filepath.Join(repo, dir))
		require.NoError(t, err)

		for _, e := range entries {
			if !e.IsDir() {
				copyFile(filepath.Join(dir, e.Name()))
			}
		}
	}

	for _, ins := range insertions(&Module{Perms: true}) {
		copyFile(ins.path)
	}

	return root
}

func read(t *testing.T, root, rel string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, rel))
	require.NoError(t, err)

	return string(data)
}

func TestNewModuleNamesTheIdentifiers(t *testing.T) {
	t.Parallel()

	m, err := NewModule("task", "", false)
	require.NoError(t, err)
	assert.Equal(t, &Module{Name: "Task", Plural: "Tasks", Lower: "task", LowerPlural: "tasks", Perms: false}, m)

	m, err = NewModule("category", "categories", true)
	require.NoError(t, err)
	assert.Equal(t, "Categories", m.Plural)

	for _, bad := range []string{"", "Task", "my_task", "1task"} {
		_, err := NewModule(bad, "", false)
		require.ErrorIs(t, err, errName, bad)
	}

	_, err = NewModule("sheep", "sheep", false)
	require.ErrorIs(t, err, errName)
}

func TestGenerateWritesTheModuleAndRegistersIt(t *testing.T) {
	t.Parallel()

	root := workspace(t)

	m, err := NewModule("widget", "", true)
	require.NoError(t, err)
	require.NoError(t, Generate(root, m))

	entity := read(t, root, "internal/entity/widget.go")
	assert.Contains(t, entity, "type Widget struct")
	assert.Regexp(t, `PermWidgetRead\s+= "widget:read"`, entity)

	assert.Contains(t, read(t, root, "internal/usecase/widget/widget.go"), "package widget")
	assert.Contains(t, read(t, root, "openapi/src/paths/widgets.yaml"), "operationId: listWidgets")
	assert.Contains(t, read(t, root, "openapi/src/paths/widgets.yaml"), "'403':")
	assert.Contains(t, read(t, root, "internal/repo/postgres/queries/widgets.sql"), "FROM widget")

	entries, err := os.ReadDir(filepath.Join(root, "migrations", "core"))
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(entries[len(entries)-1].Name(), "_widget.sql"))

	policy := read(t, root, "internal/controller/http/policy.go")
	assert.Regexp(t, `"listWidgets":\s+\{Auth: AuthJWT, Perms: All\(entity.PermWidgetRead\)\},`, policy)
	assert.Regexp(t, `"deleteWidget":\s+\{Auth: AuthJWT, Perms: All\(entity.PermWidgetWrite\), Tx: true`, policy)
	assert.Less(t, strings.Index(policy, "deleteWidget"), strings.Index(policy, "// modulegen:policies"))

	server := read(t, root, "internal/controller/http/v1/server.go")
	assert.Regexp(t, `widgets\s+usecase.Widgets`, server)
	assert.Regexp(t, `widgets:\s+d.Widgets,`, server)

	app := read(t, root, "internal/app/app.go")
	assert.Regexp(t, `Widgets:\s+widget.New\(postgres.NewWidgets\(d.db\)\),`, app)
	assert.Contains(t, app, `"github.com/xingyunqi-tech/go-template/internal/usecase/widget"`)
	assert.Less(t, strings.Index(app, "usecase/widget"), strings.Index(app, "// modulegen:imports"))
	assert.Contains(t, read(t, root, ".golangci.yml"), "- path: internal/(repo/postgres/widgets|controller/http/v1/widgets|usecase/widget/widget)\\.go")

	assert.Contains(t, read(t, root, "internal/app/purgers.go"), "postgres.NewWidgets(db),")
	assert.Regexp(t, `PermWidgetRead:\s+nil,`, read(t, root, "internal/entity/rbac.go"))
	assert.Contains(t, read(t, root, "internal/repo/contracts.go"), "type Widgets interface")
	assert.Contains(t, read(t, root, "internal/usecase/contracts.go"), "type Widgets interface")

	require.ErrorIs(t, Generate(root, m), errClash, "the module exists already")
}

func TestWithoutPermsNothingIsRequired(t *testing.T) {
	t.Parallel()

	root := workspace(t)

	m, err := NewModule("widget", "", false)
	require.NoError(t, err)
	require.NoError(t, Generate(root, m))

	assert.NotContains(t, read(t, root, "internal/entity/widget.go"), "PermWidget")
	assert.NotContains(t, read(t, root, "internal/entity/rbac.go"), "PermWidget")
	assert.NotContains(t, read(t, root, "openapi/src/paths/widgets.yaml"), "'403':")
	assert.Regexp(t, `"listWidgets":\s+\{Auth: AuthJWT\},`, read(t, root, "internal/controller/http/policy.go"))
}

func TestAMissingAnchorFails(t *testing.T) {
	t.Parallel()

	root := workspace(t)
	require.NoError(t, write(filepath.Join(root, "internal", "app", "purgers.go"), []byte("package app\n")))

	m, err := NewModule("widget", "", false)
	require.NoError(t, err)
	require.ErrorIs(t, Generate(root, m), errAnchor)
}

func TestNamesThatClashAreRefusedBeforeAnythingIsWritten(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"type", "error", "order", "role", "page", "note", "config", "worker", "audit"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := workspace(t)

			m, err := NewModule(name, "", false)
			require.NoError(t, err)
			require.ErrorIs(t, Generate(root, m), errClash)

			_, err = os.Stat(filepath.Join(root, "internal", "usecase", name))
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestAFailedInsertionWritesNothing(t *testing.T) {
	t.Parallel()

	root := workspace(t)
	require.NoError(t, write(filepath.Join(root, "internal", "app", "purgers.go"), []byte("package app\n")))

	before := read(t, root, "internal/controller/http/policy.go")

	m, err := NewModule("widget", "", false)
	require.NoError(t, err)
	require.ErrorIs(t, Generate(root, m), errAnchor)

	assert.Equal(t, before, read(t, root, "internal/controller/http/policy.go"))

	_, err = os.Stat(filepath.Join(root, "internal", "entity", "widget.go"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
