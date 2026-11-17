//go:build contract

package contract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	_contractDoc       = "docs/contract.md"
	_unimplementedHead = "## 基线中未实现的接口"
)

type baselineOperation struct {
	method, path, id, tag string
}

// orderedBaseline lists the baseline operations in document order of paths and methods.
func orderedBaseline(t *testing.T) []baselineOperation {
	t.Helper()

	var doc struct {
		Paths json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(readFile(t, "openapi/nestjs-baseline.json"), &doc))

	var out []baselineOperation

	for path, item := range orderedObject(t, doc.Paths) {
		for method, raw := range orderedObject(t, item) {
			var op struct {
				OperationID string   `json:"operationId"`
				Tags        []string `json:"tags"`
			}
			if json.Unmarshal(raw, &op) != nil || op.OperationID == "" {
				continue
			}

			tag := ""
			if len(op.Tags) > 0 {
				tag = op.Tags[0]
			}

			out = append(out, baselineOperation{method: strings.ToUpper(method), path: path, id: op.OperationID, tag: tag})
		}
	}

	return out
}

// orderedObject yields the members of a JSON object in document order.
func orderedObject(t *testing.T, raw json.RawMessage) func(func(string, json.RawMessage) bool) {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(raw))
	_, err := dec.Token()
	require.NoError(t, err)

	return func(yield func(string, json.RawMessage) bool) {
		for dec.More() {
			key, err := dec.Token()
			require.NoError(t, err)

			var value json.RawMessage
			require.NoError(t, dec.Decode(&value))

			name, _ := key.(string)
			if !yield(name, value) {
				return
			}
		}
	}
}

func unimplementedSection(ops []baselineOperation) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%s（%d 个）\n\n", _unimplementedHead, len(ops))
	b.WriteString("请求这些接口返回 404 信封 `Cannot <METHOD> <originalUrl>`。前端仍可从 `/docs-json` 生成全部代码。")
	b.WriteString("本节由 `TestUnimplementedOperationsAreListedInContractDoc` 校验，`UPDATE_CONTRACT_DOC=1 make test-contract` 重新生成。\n\n")
	b.WriteString("| 方法 | 路径 | operationId | tag |\n|---|---|---|---|\n")

	for _, op := range ops {
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", op.method, op.path, op.id, op.tag)
	}

	b.WriteString("\n")

	return b.String()
}

func TestUnimplementedOperationsAreListedInContractDoc(t *testing.T) {
	t.Parallel()

	scope := loadScope(t)

	var missing []baselineOperation

	for _, op := range orderedBaseline(t) {
		if !slices.Contains(scope, op.id) {
			missing = append(missing, op)
		}
	}

	doc := string(readFile(t, _contractDoc))
	start := strings.Index(doc, _unimplementedHead)
	require.NotEqual(t, -1, start, "%s has no %q section", _contractDoc, _unimplementedHead)

	end := strings.Index(doc[start+len(_unimplementedHead):], "\n## ")
	require.NotEqual(t, -1, end, "the %q section must be followed by another section", _unimplementedHead)
	end += start + len(_unimplementedHead) + 1

	want := unimplementedSection(missing)
	if os.Getenv("UPDATE_CONTRACT_DOC") == "1" {
		updated := doc[:start] + want + doc[end:]
		require.NoError(t, os.WriteFile(filepath.Join(root, _contractDoc), []byte(updated), 0o600))

		return
	}

	require.Equal(t, want, doc[start:end], "run UPDATE_CONTRACT_DOC=1 make test-contract to regenerate the section")
}
