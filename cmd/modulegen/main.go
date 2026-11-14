// Command modulegen scaffolds a module for the operations of one OpenAPI tag: it adds them to
// openapi/scope.yaml and writes handler stubs, a use case, a repository and its port.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/xingyunqi-tech/go-template/templates"
)

const (
	_fileMode = 0o644
	_dirMode  = 0o755
)

var (
	_packageName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

	errUsage    = errors.New("usage: modulegen -name <package> -tag <OpenAPI tag>")
	errNoTag    = errors.New("no operation has the tag")
	errExists   = errors.New("file already exists")
	errNotFound = errors.New("operation without operationId")
)

// Operation is one documented operation of the tag.
type Operation struct {
	ID         string
	Method     string
	HTTPMethod string
	Path       string
}

// Module is the template data.
type Module struct {
	Package    string
	Type       string
	Tag        string
	Operations []Operation
}

func main() {
	if err := run(os.Args[1:], ".", os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, root string, out io.Writer) error {
	fs := flag.NewFlagSet("modulegen", flag.ContinueOnError)
	name := fs.String("name", "", "package name, e.g. orders")
	tag := fs.String("tag", "", "OpenAPI tag, e.g. Orders")

	if err := fs.Parse(args); err != nil {
		return err //nolint:wrapcheck // flag errors are printed as they are
	}

	if !_packageName.MatchString(*name) || *tag == "" {
		return errUsage
	}

	ops, err := operations(filepath.Join(root, "openapi", "nestjs-baseline.json"), *tag)
	if err != nil {
		return err
	}

	m := Module{Package: *name, Type: exported(*name), Tag: *tag, Operations: ops}

	if err := scaffold(root, &m); err != nil {
		return err
	}

	if err := addToScope(filepath.Join(root, "openapi", "scope.yaml"), ops); err != nil {
		return err
	}

	return printNextSteps(out, &m)
}

// scaffold writes the module files and the repository port, refusing to overwrite.
func scaffold(root string, m *Module) error {
	files := map[string]string{
		filepath.Join("internal", "controller", "http", "v1", m.Package+".go"): "handlers.go.tmpl",
		filepath.Join("internal", "usecase", m.Package, m.Package+".go"):       "usecase.go.tmpl",
		filepath.Join("internal", "repo", "postgres", m.Package+".go"):         "repo.go.tmpl",
	}

	for path := range files {
		if _, err := os.Stat(filepath.Join(root, path)); err == nil {
			return fmt.Errorf("%w: %s", errExists, path)
		}
	}

	for path, tmpl := range files {
		if err := write(filepath.Join(root, path), tmpl, m); err != nil {
			return err
		}
	}

	return appendPort(filepath.Join(root, "internal", "repo", "contracts.go"), m)
}

func exported(name string) string {
	r := []rune(name)
	r[0] = unicode.ToUpper(r[0])

	return string(r)
}

func operations(specPath, tag string) ([]Operation, error) {
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("modulegen: %w", err)
	}

	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string   `json:"operationId"`
			Tags        []string `json:"tags"`
		} `json:"paths"`
	}

	if err := json.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("modulegen: %s: %w", specPath, err)
	}

	var ops []Operation

	for path, methods := range spec.Paths {
		for method, op := range methods {
			if !slices.Contains(op.Tags, tag) {
				continue
			}

			if op.OperationID == "" {
				return nil, fmt.Errorf("%w: %s %s", errNotFound, method, path)
			}

			ops = append(ops, Operation{
				ID: op.OperationID, Method: exported(op.OperationID), HTTPMethod: strings.ToUpper(method), Path: path,
			})
		}
	}

	if len(ops) == 0 {
		return nil, fmt.Errorf("%w: %s", errNoTag, tag)
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].ID < ops[j].ID })

	return ops, nil
}

func render(name string, m *Module) ([]byte, error) {
	t, err := template.ParseFS(templates.Module, "module/"+name)
	if err != nil {
		return nil, fmt.Errorf("modulegen: %w", err)
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, m); err != nil {
		return nil, fmt.Errorf("modulegen: %s: %w", name, err)
	}

	return buf.Bytes(), nil
}

func write(path, tmpl string, m *Module) error {
	src, err := render(tmpl, m)
	if err != nil {
		return err
	}

	formatted, err := format.Source(src)
	if err != nil {
		return fmt.Errorf("modulegen: format %s: %w", path, err)
	}

	if err := os.MkdirAll(filepath.Dir(path), _dirMode); err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	if err := os.WriteFile(path, formatted, _fileMode); err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	return nil
}

func appendPort(path string, m *Module) error {
	port, err := render("port.go.tmpl", m)
	if err != nil {
		return err
	}

	existing, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	formatted, err := format.Source(append(existing, port...))
	if err != nil {
		return fmt.Errorf("modulegen: format %s: %w", path, err)
	}

	if err := os.WriteFile(path, formatted, _fileMode); err != nil { //nolint:gosec // paths come from the repository layout
		return fmt.Errorf("modulegen: %w", err)
	}

	return nil
}

// addToScope appends the operation ids missing from scope.yaml.
func addToScope(path string, ops []Operation) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	listed := map[string]bool{}

	for line := range strings.SplitSeq(string(raw), "\n") {
		if id, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok {
			listed[id] = true
		}
	}

	out := strings.TrimRight(string(raw), "\n") + "\n"

	for _, op := range ops {
		if !listed[op.ID] {
			out += "  - " + op.ID + "\n"
		}
	}

	if err := os.WriteFile(path, []byte(out), _fileMode); err != nil { //nolint:gosec // paths come from the repository layout
		return fmt.Errorf("modulegen: %w", err)
	}

	return nil
}

func printNextSteps(out io.Writer, m *Module) error {
	var b strings.Builder

	b.WriteString("Generated the " + m.Tag + " module. Next:\n")
	b.WriteString("  1. make generate\n")
	b.WriteString("  2. declare the operations in internal/controller/http/policy.go:\n")

	for _, op := range m.Operations {
		fmt.Fprintf(&b, "       %q: {Auth: AuthJWT}, // %s %s\n", op.ID, op.HTTPMethod, op.Path)
	}

	b.WriteString("  3. add queries in internal/repo/postgres/queries/" + m.Package + ".sql and wire the service in internal/app\n")

	if _, err := io.WriteString(out, b.String()); err != nil {
		return fmt.Errorf("modulegen: %w", err)
	}

	return nil
}
