package http

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/go-chi/chi/v5"
)

var errPolicy = errors.New("policy")

// operation is one documented route.
type operation struct {
	id     string
	method string
	// pattern is the chi route pattern, the OpenAPI path.
	pattern string
	policy  Policy
	route   *routers.Route
	// idempotent operations declare the Idempotency-Key header.
	idempotent bool
	// mediaTypes are the request body content types; nil without a request body.
	mediaTypes []string
	// bodyRequired reports requestBody.required.
	bodyRequired bool
}

// operations indexes the documented routes by method and pattern.
type operations struct {
	byRoute map[string]*operation
}

func routeKey(method, pattern string) string {
	return method + " " + pattern
}

func newOperation(doc *openapi3.T, path string, item *openapi3.PathItem, method string, op *openapi3.Operation, policy *Policy) *operation {
	o := &operation{
		id: op.OperationID, method: method, pattern: path, policy: *policy,
		route:      &routers.Route{Spec: doc, Server: nil, Path: path, PathItem: item, Method: method, Operation: op},
		idempotent: false, mediaTypes: nil, bodyRequired: false,
	}

	for _, p := range op.Parameters {
		if p.Value != nil && p.Value.In == openapi3.ParameterInHeader && strings.EqualFold(p.Value.Name, "Idempotency-Key") {
			o.idempotent = true
		}
	}

	if op.RequestBody != nil && op.RequestBody.Value != nil {
		o.mediaTypes = slices.Sorted(maps.Keys(op.RequestBody.Value.Content))
		o.bodyRequired = op.RequestBody.Value.Required
	}

	return o
}

// loadOperations reads the validation document and pairs every operation with its policy.
func loadOperations(ctx context.Context, document []byte, policies map[string]Policy) (*operations, error) {
	loader := openapi3.NewLoader()

	doc, err := loader.LoadFromData(document)
	if err != nil {
		return nil, fmt.Errorf("load validation document: %w", err)
	}

	if err := doc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validation document: %w", err)
	}

	ops := &operations{byRoute: map[string]*operation{}}

	var errs []error

	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Value(path)

		for method, op := range item.Operations() {
			policy, ok := policies[op.OperationID]
			if !ok {
				errs = append(errs, fmt.Errorf("%w: %s %s (%s) has no policy", errPolicy, method, path, op.OperationID))

				continue
			}

			if err := checkRateLimits(op.OperationID, &policy); err != nil {
				errs = append(errs, err)
			}

			o := newOperation(doc, path, item, method, op, &policy)
			ops.byRoute[routeKey(method, path)] = o
		}
	}

	for id := range policies {
		if !ops.has(id) {
			errs = append(errs, fmt.Errorf("%w: %s is not in the document", errPolicy, id))
		}
	}

	return ops, errors.Join(errs...)
}

func (o *operations) has(id string) bool {
	for _, op := range o.byRoute {
		if op.id == id {
			return true
		}
	}

	return false
}

func (o *operations) ids() []string {
	ids := make([]string, 0, len(o.byRoute))
	for _, op := range o.byRoute {
		ids = append(ids, op.id)
	}

	slices.Sort(ids)

	return ids
}

// checkRegistered reports documented operations without a registered handler.
func (o *operations) checkRegistered(mux chi.Routes) error {
	registered := map[string]bool{}

	err := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		registered[routeKey(method, route)] = true

		return nil
	})
	if err != nil {
		return fmt.Errorf("walk routes: %w", err)
	}

	var errs []error

	for key, op := range o.byRoute {
		if !registered[key] {
			errs = append(errs, fmt.Errorf("%w: %s has no handler", errPolicy, op.id))
		}
	}

	return errors.Join(errs...)
}

// lookup finds the route of method and path: the matched pattern and path parameters, the
// documented operation when it is one, and the allowed methods when only the method differs.
func lookup(mux *chi.Mux, method, path string) (pattern string, params map[string]string, allowed []string) {
	rctx := chi.NewRouteContext()

	if pattern = mux.Find(rctx, method, path); pattern != "" {
		params = make(map[string]string, len(rctx.URLParams.Keys))
		for i, k := range rctx.URLParams.Keys {
			params[k] = rctx.URLParams.Values[i]
		}

		return pattern, params, nil
	}

	for _, m := range []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		if m != method && mux.Find(chi.NewRouteContext(), m, path) != "" {
			allowed = append(allowed, m)
		}
	}

	return "", nil, allowed
}
