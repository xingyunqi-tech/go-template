package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	_jsonMediaType = "application/json"
	_schemaRefRoot = "#/components/schemas/"
)

var errSpec = errors.New("openapi document")

// operation is one documented operation with what the request pipeline needs from the document.
type operation struct {
	id         string
	method     string
	pattern    string
	pathParams []pathParam
	bodyDTO    string
	bearer     bool
}

type pathParam struct {
	name    string
	numeric bool
}

type specParameter struct {
	In     string `json:"in"`
	Name   string `json:"name"`
	Schema struct {
		Type string `json:"type"`
	} `json:"schema"`
}

type specOperation struct {
	OperationID string             `json:"operationId"`
	Parameters  []specParameter    `json:"parameters"`
	Security    []map[string][]any `json:"security"`
	RequestBody *specRequestBody   `json:"requestBody"`
}

type specRequestBody struct {
	Content map[string]struct {
		Schema struct {
			Ref string `json:"$ref"`
		} `json:"schema"`
	} `json:"content"`
}

// parseOperations reads every operation of the OpenAPI document, keyed by "METHOD pattern".
func parseOperations(doc []byte) (map[string]*operation, error) {
	var spec struct {
		Paths map[string]map[string]specOperation `json:"paths"`
	}

	if err := json.Unmarshal(doc, &spec); err != nil {
		return nil, fmt.Errorf("%w: %w", errSpec, err)
	}

	ops := map[string]*operation{}

	for pattern, methods := range spec.Paths {
		for method, raw := range methods {
			op := newOperation(strings.ToUpper(method), pattern, &raw)
			ops[operationKey(op.method, pattern)] = op
		}
	}

	return ops, nil
}

func newOperation(method, pattern string, raw *specOperation) *operation {
	op := &operation{
		id:         raw.OperationID,
		method:     method,
		pattern:    pattern,
		pathParams: nil,
		bodyDTO:    "",
		bearer:     false,
	}

	for _, p := range raw.Parameters {
		if p.In == "path" {
			op.pathParams = append(op.pathParams, pathParam{name: p.Name, numeric: p.Schema.Type == "number"})
		}
	}

	for _, sec := range raw.Security {
		if _, ok := sec["bearer"]; ok {
			op.bearer = true
		}
	}

	if raw.RequestBody != nil {
		if content, ok := raw.RequestBody.Content[_jsonMediaType]; ok {
			op.bodyDTO = strings.TrimPrefix(content.Schema.Ref, _schemaRefRoot)
		}
	}

	return op
}

func operationKey(method, pattern string) string {
	if method == http.MethodHead {
		method = http.MethodGet
	}

	return method + " " + pattern
}
