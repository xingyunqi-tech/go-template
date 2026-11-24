package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
)

// identityTags are the tags of the operations only the issuing service serves (platform
// conventions §11.1–§11.3).
func identityTags() []string {
	return []string{"auth", "me", "users"}
}

// withoutTags returns the OpenAPI document doc without the operations tagged with one of tags,
// the paths left empty and the components nothing references any more.
func withoutTags(doc []byte, tags []string) ([]byte, error) {
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("app: document: %w", err)
	}

	paths, _ := root["paths"].(map[string]any)

	for path, raw := range paths {
		item, _ := raw.(map[string]any)

		for method, op := range item {
			if tagged(op, tags) {
				delete(item, method)
			}
		}

		if !hasOperation(item) {
			delete(paths, path)
		}
	}

	pruneComponents(root)

	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("app: document: %w", err)
	}

	return out, nil
}

const _refPrefix = "#/components/"

// pruneComponents drops the components no path reaches through $ref; security schemes are named
// by the security requirements and stay.
func pruneComponents(root map[string]any) {
	components, _ := root["components"].(map[string]any)
	refs := &references{components: components, reached: map[string]bool{}}

	refs.walk(root["paths"])

	for kind, raw := range components {
		group, ok := raw.(map[string]any)
		if !ok || kind == "securitySchemes" {
			continue
		}

		for name := range group {
			if !refs.reached[_refPrefix+kind+"/"+name] {
				delete(group, name)
			}
		}
	}
}

// references collects the components a part of the document reaches, following each $ref once.
type references struct {
	components map[string]any
	reached    map[string]bool
}

func (r *references) walk(v any) {
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok {
			r.follow(ref)
		}

		for _, child := range x {
			r.walk(child)
		}
	case []any:
		for _, child := range x {
			r.walk(child)
		}
	}
}

func (r *references) follow(ref string) {
	if !strings.HasPrefix(ref, _refPrefix) || r.reached[ref] {
		return
	}

	r.reached[ref] = true

	kind, name, _ := strings.Cut(strings.TrimPrefix(ref, _refPrefix), "/")
	if group, ok := r.components[kind].(map[string]any); ok {
		r.walk(group[name])
	}
}

func tagged(op any, tags []string) bool {
	m, ok := op.(map[string]any)
	if !ok {
		return false
	}

	list, _ := m["tags"].([]any)

	return slices.ContainsFunc(list, func(t any) bool {
		s, _ := t.(string)

		return slices.Contains(tags, s)
	})
}

func hasOperation(item map[string]any) bool {
	for key := range item {
		if key != "parameters" && key != "summary" && key != "description" {
			return true
		}
	}

	return false
}

// operationIDs lists the operationIds of an OpenAPI document.
func operationIDs(doc []byte) (map[string]bool, error) {
	var root struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}

	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("app: document: %w", err)
	}

	ids := map[string]bool{}

	for _, item := range root.Paths {
		for _, raw := range item {
			var op struct {
				OperationID string `json:"operationId"`
			}

			if json.Unmarshal(raw, &op) == nil && op.OperationID != "" {
				ids[op.OperationID] = true
			}
		}
	}

	return ids, nil
}

// servedDocuments returns the published and validation documents and the policies of the
// operations this service serves: a verifier leaves out the identity operations.
func servedDocuments(published, validation []byte, verifier bool) (pub, val []byte, policies map[string]httpapi.Policy, err error) {
	all := httpapi.Policies()
	if !verifier {
		return published, validation, all, nil
	}

	if pub, err = withoutTags(published, identityTags()); err != nil {
		return nil, nil, nil, err
	}

	if val, err = withoutTags(validation, identityTags()); err != nil {
		return nil, nil, nil, err
	}

	ids, err := operationIDs(val)
	if err != nil {
		return nil, nil, nil, err
	}

	policies = map[string]httpapi.Policy{}

	for id, p := range all {
		if ids[id] {
			policies[id] = p
		}
	}

	return pub, val, policies, nil
}
