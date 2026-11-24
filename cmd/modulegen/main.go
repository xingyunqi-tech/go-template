// Command modulegen adds a CRUD module modeled on note: spec, core migration, sqlc queries, repo,
// use case, handler, Purger, policy entries and tests, then runs make generate.
//
// Usage: go run ./cmd/modulegen -name task [-plural tasks] [-perms] [-no-generate]
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

const (
	_fileMode = 0o644
	_dirMode  = 0o755
)

var (
	errName   = errors.New("modulegen: -name must be a lower-case Go identifier, such as task")
	errClash  = errors.New("modulegen: name clashes")
	errExists = errors.New("modulegen: file exists")
	errAnchor = errors.New("modulegen: anchor not found")

	_name      = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	_migration = regexp.MustCompile(`^(\d{5})_.*\.sql$`)
)

// Module names a generated module.
type Module struct {
	Name, Plural, Lower, LowerPlural string
	Perms                            bool
}

func (m *Module) data() map[string]any {
	return map[string]any{"Name": m.Name, "Plural": m.Plural, "name": m.Lower, "plural": m.LowerPlural, "Perms": m.Perms}
}

// NewModule derives the identifiers of name; plural "" is name + "s".
func NewModule(name, plural string, perms bool) (*Module, error) {
	if plural == "" {
		plural = name + "s"
	}

	if !_name.MatchString(name) || !_name.MatchString(plural) || name == plural {
		return nil, errName
	}

	return &Module{Name: title(name), Plural: title(plural), Lower: name, LowerPlural: plural, Perms: perms}, nil
}

func title(s string) string {
	return strings.ToUpper(s[:1]) + s[1:]
}

func main() {
	name := flag.String("name", "", "module name, lower case and singular")
	plural := flag.String("plural", "", "plural of the name (name + s by default)")
	perms := flag.Bool("perms", false, "register <name>:read and <name>:write and require them")
	noGenerate := flag.Bool("no-generate", false, "skip make generate")

	flag.Parse()

	m, err := NewModule(*name, *plural, *perms)
	if err == nil {
		err = Generate(".", m)
	}

	if err == nil && !*noGenerate {
		err = run(context.Background(), "make", "generate")
	}

	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("modulegen: %s: %w", name, err)
	}

	return nil
}

// target is a generated file and the template it is rendered from.
type target struct {
	template, path string
}

func targets(root string, m *Module) ([]target, error) {
	migration, err := nextMigration(filepath.Join(root, "migrations", "core"), m.Lower)
	if err != nil {
		return nil, err
	}

	return []target{
		{"entity.go.tmpl", filepath.Join("internal", "entity", m.Lower+".go")},
		{"postgres.go.tmpl", filepath.Join("internal", "repo", "postgres", m.LowerPlural+".go")},
		{"queries.sql.tmpl", filepath.Join("internal", "repo", "postgres", "queries", m.LowerPlural+".sql")},
		{"usecase.go.tmpl", filepath.Join("internal", "usecase", m.Lower, m.Lower+".go")},
		{"usecase_test.go.tmpl", filepath.Join("internal", "usecase", m.Lower, m.Lower+"_test.go")},
		{"handler.go.tmpl", filepath.Join("internal", "controller", "http", "v1", m.LowerPlural+".go")},
		{"paths.yaml.tmpl", filepath.Join("openapi", "src", "paths", m.LowerPlural+".yaml")},
		{"schemas.yaml.tmpl", filepath.Join("openapi", "src", "schemas", m.LowerPlural+".yaml")},
		{"migration.sql.tmpl", filepath.Join("migrations", "core", migration)},
		{"integration_test.go.tmpl", filepath.Join("internal", "app", m.LowerPlural+"_integration_test.go")},
	}, nil
}

// nextMigration names the next goose migration of dir.
func nextMigration(dir, name string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("modulegen: %w", err)
	}

	last := 0

	for _, e := range entries {
		if match := _migration.FindStringSubmatch(e.Name()); match != nil {
			n, _ := strconv.Atoi(match[1])
			last = max(last, n)
		}
	}

	return fmt.Sprintf("%05d_%s.sql", last+1, name), nil
}

// Generate writes the module files under root and registers the module at the modulegen
// anchors. Every file is rendered and edited in memory first, so nothing is written when a step
// fails; it refuses names that clash with Go, SQL or the existing code.
func Generate(root string, m *Module) error {
	if err := checkName(root, m); err != nil {
		return err
	}

	ts, err := targets(root, m)
	if err != nil {
		return err
	}

	plan := newPlan(root)

	for _, t := range ts {
		if _, err := os.Stat(filepath.Join(root, t.path)); err == nil {
			return fmt.Errorf("%w: %s", errExists, t.path)
		}

		out, err := render(root, t.template, m)
		if err != nil {
			return err
		}

		plan.files[t.path] = out
	}

	if err := register(plan, m); err != nil {
		return err
	}

	return plan.write()
}

// plan holds the new content of every file Generate touches, keyed by path relative to root.
type plan struct {
	root  string
	files map[string][]byte
}

func newPlan(root string) *plan {
	return &plan{root: root, files: map[string][]byte{}}
}

// content returns the planned content of path, read from disk the first time.
func (p *plan) content(path string) ([]byte, error) {
	if data, ok := p.files[path]; ok {
		return data, nil
	}

	data, err := os.ReadFile(filepath.Join(p.root, path))
	if err != nil {
		return nil, fmt.Errorf("modulegen: %w", err)
	}

	return data, nil
}

func (p *plan) write() error {
	paths := make([]string, 0, len(p.files))
	for path := range p.files {
		paths = append(paths, path)
	}

	slices.Sort(paths)

	for _, path := range paths {
		if err := write(filepath.Join(p.root, path), p.files[path]); err != nil {
			return err
		}
	}

	return nil
}

func render(root, name string, m *Module) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(root, "templates", "module", name))
	if err != nil {
		return nil, fmt.Errorf("modulegen: %w", err)
	}

	return renderText(name, string(raw), m)
}

func renderText(name, text string, m *Module) ([]byte, error) {
	tmpl, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("modulegen: %s: %w", name, err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, m.data()); err != nil {
		return nil, fmt.Errorf("modulegen: %s: %w", name, err)
	}

	if !strings.HasSuffix(name, ".go.tmpl") {
		return buf.Bytes(), nil
	}

	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("modulegen: %s: %w", name, err)
	}

	return out, nil
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), _dirMode); err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	if err := os.WriteFile(path, data, _fileMode); err != nil { //nolint:gosec // paths under the repository the developer runs it in
		return fmt.Errorf("modulegen: %w", err)
	}

	return nil
}

// insertion adds text, or the template snippet when it is set, before the "modulegen:<anchor>"
// comment line of a file, or at the end of the file when anchor is empty.
type insertion struct {
	path, anchor, text, snippet string
}

func insertions(m *Module) []insertion {
	read, write := "", ""
	if m.Perms {
		read = ", Perms: All(entity.Perm{{.Name}}Read)"
		write = ", Perms: All(entity.Perm{{.Name}}Write)"
	}

	policies := `"list{{.Plural}}":  {Auth: AuthJWT` + read + `},
"create{{.Name}}": {Auth: AuthJWT` + write + `, Tx: true, Audit: &Audit{Action: "{{.name}}.create", Resource: "{{.name}}"}},
"get{{.Name}}":    {Auth: AuthJWT` + read + `},
"update{{.Name}}": {Auth: AuthJWT` + write + `, Tx: true, Audit: &Audit{Action: "{{.name}}.update", Resource: "{{.name}}"}},
"delete{{.Name}}": {Auth: AuthJWT` + write + `, Tx: true, Audit: &Audit{Action: "{{.name}}.delete", Resource: "{{.name}}"}},
`

	dupl := `- path: internal/(repo/postgres/{{.plural}}|controller/http/v1/{{.plural}}|usecase/{{.name}}/{{.name}})\.go
  linters:
    - dupl
`

	out := []insertion{
		{filepath.Join("internal", "controller", "http", "policy.go"), "policies", policies, ""},
		{filepath.Join("internal", "controller", "http", "v1", "server.go"), "fields", "{{.plural}} usecase.{{.Plural}}\n", ""},
		{filepath.Join("internal", "controller", "http", "v1", "server.go"), "deps", "{{.Plural}} usecase.{{.Plural}}\n", ""},
		{filepath.Join("internal", "controller", "http", "v1", "server.go"), "new", "{{.plural}}: d.{{.Plural}},\n", ""},
		{filepath.Join("internal", "app", "app.go"), "modules", "{{.Plural}}: {{.name}}.New(postgres.New{{.Plural}}(d.db)),\n", ""},
		{filepath.Join("internal", "app", "app.go"), "imports", "\"github.com/xingyunqi-tech/go-template/internal/usecase/{{.name}}\"\n", ""},
		{filepath.Join("internal", "app", "purgers.go"), "purgers", "postgres.New{{.Plural}}(db),\n", ""},
		{".golangci.yml", "dupl", dupl, ""},
		{filepath.Join("internal", "repo", "contracts.go"), "", "", "repo_contracts.go.tmpl"},
		{filepath.Join("internal", "usecase", "contracts.go"), "", "", "usecase_contracts.go.tmpl"},
	}

	if m.Perms {
		out = append(out, insertion{
			filepath.Join("internal", "entity", "rbac.go"), "permissions", "Perm{{.Name}}Read:  nil,\nPerm{{.Name}}Write: nil,\n", "",
		})
	}

	return out
}

func register(p *plan, m *Module) error {
	for _, ins := range insertions(m) {
		if err := p.apply(m, &ins); err != nil {
			return err
		}
	}

	return nil
}

func (p *plan) apply(m *Module, ins *insertion) error {
	text := ins.text

	if ins.snippet != "" {
		raw, err := os.ReadFile(filepath.Join(p.root, "templates", "module", ins.snippet))
		if err != nil {
			return fmt.Errorf("modulegen: %w", err)
		}

		text = string(raw)
	}

	rendered, err := renderText(ins.path, text, m)
	if err != nil {
		return err
	}

	current, err := p.content(ins.path)
	if err != nil {
		return err
	}

	out, err := insert(ins.path, current, ins.anchor, string(rendered))
	if err != nil {
		return err
	}

	p.files[ins.path] = out

	return nil
}

// insert returns content with text added before the anchor line, indented like it, or at the
// end; Go files are formatted.
func insert(path string, content []byte, anchor, text string) ([]byte, error) {
	lines := strings.SplitAfter(string(content), "\n")

	if anchor == "" {
		lines = append(lines, text)
	} else {
		i := slices.IndexFunc(lines, func(l string) bool {
			trimmed := strings.TrimSpace(l)

			return trimmed == "// modulegen:"+anchor || trimmed == "# modulegen:"+anchor
		})
		if i < 0 {
			return nil, fmt.Errorf("%w: %s in %s", errAnchor, anchor, path)
		}

		indent := lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
		lines = slices.Insert(lines, i, indented(text, indent))
	}

	out := []byte(strings.Join(lines, ""))
	if !strings.HasSuffix(path, ".go") {
		return out, nil
	}

	formatted, err := format.Source(out)
	if err != nil {
		return nil, fmt.Errorf("modulegen: %s: %w", path, err)
	}

	return formatted, nil
}

func indented(text, indent string) string {
	var b strings.Builder

	for line := range strings.Lines(text) {
		if strings.TrimSpace(line) != "" {
			b.WriteString(indent)
		}

		b.WriteString(line)
	}

	return b.String()
}

// sqlReserved are the PostgreSQL reserved key words, which cannot name a table unquoted.
func sqlReserved() []string {
	return strings.Fields(`all analyze and any array as asc asymmetric authorization binary both case cast
check collate collation column concurrently constraint create cross current_catalog current_date current_role
current_schema current_time current_timestamp current_user default deferrable desc distinct do else end except
false fetch for foreign freeze from full grant group having ilike in initially inner intersect into is isnull
join lateral leading left like limit localtime localtimestamp natural not notnull null offset on only or order
outer overlaps placing primary references returning right select session_user similar some symmetric
system_user table tablesample then to trailing true union unique user using variadic verbose when where window
with`)
}

// checkName rejects a module whose identifiers are Go keywords or predeclared, a reserved SQL
// word, or already declared by the entity package or the spec.
func checkName(root string, m *Module) error {
	for _, n := range []string{m.Lower, m.LowerPlural} {
		if token.IsKeyword(n) || types.Universe.Lookup(n) != nil {
			return fmt.Errorf("%w: %s is a Go keyword or predeclared identifier", errClash, n)
		}
	}

	if slices.Contains(sqlReserved(), m.Lower) {
		return fmt.Errorf("%w: %s is a reserved PostgreSQL word", errClash, m.Lower)
	}

	if err := checkEntity(filepath.Join(root, "internal", "entity"), m); err != nil {
		return err
	}

	return checkSpec(filepath.Join(root, "openapi", "src"), m)
}

func checkEntity(dir string, m *Module) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	taken := []string{m.Name, m.Name + "Changes", m.Plural}
	fset := token.NewFileSet()

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}

		if err := checkFile(fset, filepath.Join(dir, e.Name()), taken); err != nil {
			return err
		}
	}

	return nil
}

func checkFile(fset *token.FileSet, path string, taken []string) error {
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	for _, decl := range f.Decls {
		for _, name := range declNames(decl) {
			if slices.Contains(taken, name) {
				return fmt.Errorf("%w: entity.%s exists", errClash, name)
			}
		}
	}

	return nil
}

func declNames(decl ast.Decl) []string {
	var names []string

	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil {
			names = append(names, d.Name.Name)
		}
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					names = append(names, n.Name)
				}
			}
		}
	}

	return names
}

// checkSpec rejects a path or component the module would add to the spec twice.
func checkSpec(dir string, m *Module) error {
	components := []string{
		m.Name, m.Name + "Page", "Create" + m.Name + "Input", "Update" + m.Name + "Input", m.Name + "ID",
		m.Name + "Envelope", m.Name + "PageEnvelope",
	}
	pattern := regexp.MustCompile(`(?m)^  /api/v1/` + m.LowerPlural + `[/:]|^    (` + strings.Join(components, "|") + `):\s*$`)

	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}

		data, err := os.ReadFile(path) //nolint:gosec // spec files of the repository
		if err != nil {
			return fmt.Errorf("modulegen: %w", err)
		}

		if match := pattern.Find(data); match != nil {
			return fmt.Errorf("%w: %s already has %q", errClash, path, strings.TrimSpace(string(match)))
		}

		return nil
	})
}
