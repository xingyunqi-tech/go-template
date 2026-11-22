package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

const (
	_root         = "openapi.yaml"
	_envelopeKey  = "x-envelope"
	_jsonMedia    = "application/json"
	_refKey       = "$ref"
	_schemasRef   = "#/components/schemas/"
	_maxRefDepth  = 64
	_successCode  = 0
	_successText  = "ok"
	_nullEnvelope = "NullEnvelope"
	_apiPrefix    = "/api/"
)

var (
	errDuplicate = errors.New("defined twice")
	errRef       = errors.New("unresolvable $ref")
	errCycle     = errors.New("$ref cycle")
	errTopLevel  = errors.New("only paths and components are merged")
	errNotMap    = errors.New("must be a map")
	errEnvelope  = errors.New("x-envelope needs a JSON schema: a component $ref, an array of one, or type null")
)

// Bundle reads openapi.yaml and merges the paths and components of every other YAML file under
// src into it, then expands the x-envelope responses.
func Bundle(src string) (map[string]any, error) {
	doc, err := readYAML(filepath.Join(src, _root))
	if err != nil {
		return nil, err
	}

	parts, err := sourceFiles(src)
	if err != nil {
		return nil, err
	}

	for _, path := range parts {
		if err := mergeFile(doc, path); err != nil {
			return nil, err
		}
	}

	if err := expandEnvelopes(doc); err != nil {
		return nil, err
	}

	addPipelineResponses(doc)

	return doc, nil
}

// pipelineResponses are answered by the request pipeline of every /api operation: the rate
// limits and an unavailable Redis or database.
func pipelineResponses() map[string]string {
	return map[string]string{
		"429": "#/components/responses/TooManyRequests",
		"503": "#/components/responses/ServiceUnavailable",
	}
}

// addPipelineResponses declares the pipelineResponses on every /api operation that lacks them.
func addPipelineResponses(doc map[string]any) {
	paths, _ := doc["paths"].(map[string]any)

	for path, item := range paths {
		if !strings.HasPrefix(path, _apiPrefix) {
			continue
		}

		methods, _ := item.(map[string]any)

		for _, op := range methods {
			declarePipelineResponses(op)
		}
	}
}

func declarePipelineResponses(op any) {
	o, _ := op.(map[string]any)

	responses, ok := o["responses"].(map[string]any)
	if !ok {
		return
	}

	for code, ref := range pipelineResponses() {
		if _, declared := responses[code]; !declared {
			responses[code] = map[string]any{_refKey: ref}
		}
	}
}

// sourceFiles lists the YAML files of src other than the root, sorted.
func sourceFiles(src string) ([]string, error) {
	var parts []string

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(path, ".yaml") && path != filepath.Join(src, _root) {
			parts = append(parts, path)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", src, err)
	}

	slices.Sort(parts)

	return parts, nil
}

func mergeFile(doc map[string]any, path string) error {
	part, err := readYAML(path)
	if err != nil {
		return err
	}

	for key, value := range part {
		src, ok := value.(map[string]any)

		switch {
		case key != "paths" && key != "components":
			return fmt.Errorf("%s: top-level key %q: %w", path, key, errTopLevel)
		case !ok:
			return fmt.Errorf("%s: %s %w", path, key, errNotMap)
		case key == "paths":
			err = mergeEntries(child(doc, "paths"), src, path+": path")
		default:
			err = mergeComponents(child(doc, "components"), src, path)
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func readYAML(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var out map[string]any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}

// child returns m[key] as a map, creating it when absent.
func child(m map[string]any, key string) map[string]any {
	c, ok := m[key].(map[string]any)
	if !ok {
		c = map[string]any{}
		m[key] = c
	}

	return c
}

// mergeEntries copies src into dst, refusing names dst already has.
func mergeEntries(dst, src map[string]any, what string) error {
	for name, entry := range src {
		if _, exists := dst[name]; exists {
			return fmt.Errorf("%s %s %w", what, name, errDuplicate)
		}

		dst[name] = entry
	}

	return nil
}

func mergeComponents(dst, src map[string]any, path string) error {
	for kind, entries := range src {
		entryMap, ok := entries.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: components.%s %w", path, kind, errNotMap)
		}

		if err := mergeEntries(child(dst, kind), entryMap, path+": components."+kind); err != nil {
			return err
		}
	}

	return nil
}

// expandEnvelopes replaces the schema of every response marked x-envelope: true with a
// reference to the success envelope {code: 0, message: "ok", data: <schema>}.
func expandEnvelopes(doc map[string]any) error {
	paths, _ := doc["paths"].(map[string]any)
	schemas := child(child(doc, "components"), "schemas")

	for _, path := range slices.Sorted(maps.Keys(paths)) {
		item, _ := paths[path].(map[string]any)

		for _, method := range slices.Sorted(maps.Keys(item)) {
			if err := expandOperation(item[method], schemas, method+" "+path); err != nil {
				return err
			}
		}
	}

	return nil
}

func expandOperation(op any, schemas map[string]any, where string) error {
	o, ok := op.(map[string]any)
	if !ok {
		return nil
	}

	responses, _ := o["responses"].(map[string]any)

	for _, code := range slices.Sorted(maps.Keys(responses)) {
		resp, _ := responses[code].(map[string]any)
		if resp[_envelopeKey] != true {
			continue
		}

		delete(resp, _envelopeKey)

		if err := wrap(resp, schemas); err != nil {
			return fmt.Errorf("%s %s: %w", where, code, err)
		}
	}

	return nil
}

func wrap(resp, schemas map[string]any) error {
	content, _ := resp["content"].(map[string]any)
	media, _ := content[_jsonMedia].(map[string]any)

	data, ok := media["schema"].(map[string]any)
	if !ok {
		return errEnvelope
	}

	name, ok := envelopeName(data)
	if !ok {
		return errEnvelope
	}

	schemas[name] = map[string]any{
		"type":     "object",
		"required": []any{"code", "message", "data"},
		"properties": map[string]any{
			"code":    map[string]any{"type": "integer", "const": _successCode},
			"message": map[string]any{"type": "string", "const": _successText},
			"data":    data,
		},
	}
	media["schema"] = map[string]any{_refKey: _schemasRef + name}

	return nil
}

// componentName is the schema name of a $ref to #/components/schemas.
func componentName(schema any) (string, bool) {
	m, _ := schema.(map[string]any)
	ref, _ := m[_refKey].(string)

	return strings.CutPrefix(ref, _schemasRef)
}

func envelopeName(data map[string]any) (string, bool) {
	if name, ok := componentName(data); ok {
		return name + "Envelope", true
	}

	switch data["type"] {
	case "null":
		return _nullEnvelope, true
	case "array":
		if name, ok := componentName(data["items"]); ok {
			return name + "ListEnvelope", true
		}
	}

	return "", false
}

// Inline returns a copy of doc in which every local $ref is replaced by its target.
func Inline(doc map[string]any) (map[string]any, error) {
	out, err := inline(doc, doc, 0)
	if err != nil {
		return nil, err
	}

	m, _ := out.(map[string]any)

	return m, nil
}

func inline(root map[string]any, v any, depth int) (any, error) {
	if depth > _maxRefDepth {
		return nil, errCycle
	}

	switch t := v.(type) {
	case map[string]any:
		return inlineMap(root, t, depth)
	case []any:
		out := make([]any, len(t))

		for i, val := range t {
			inlined, err := inline(root, val, depth)
			if err != nil {
				return nil, err
			}

			out[i] = inlined
		}

		return out, nil
	default:
		return v, nil
	}
}

func inlineMap(root, m map[string]any, depth int) (any, error) {
	if ref, ok := m[_refKey].(string); ok {
		target, err := resolve(root, ref)
		if err != nil {
			return nil, err
		}

		return inline(root, target, depth+1)
	}

	out := make(map[string]any, len(m))

	for k, val := range m {
		inlined, err := inline(root, val, depth)
		if err != nil {
			return nil, err
		}

		out[k] = inlined
	}

	return out, nil
}

func resolve(root map[string]any, ref string) (any, error) {
	pointer, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil, fmt.Errorf("%w: %s", errRef, ref)
	}

	var cur any = root

	for token := range strings.SplitSeq(pointer, "/") {
		token = strings.NewReplacer("~1", "/", "~0", "~").Replace(token)

		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %s", errRef, ref)
		}

		if cur, ok = m[token]; !ok {
			return nil, fmt.Errorf("%w: %s", errRef, ref)
		}
	}

	return cur, nil
}

// Marshal writes v as indented JSON with sorted keys and a trailing newline.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}

	return buf.Bytes(), nil
}
