package http

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
)

// _apiPrefix starts the generated routes; one left out of the document is not served.
const _apiPrefix = "/api/"

// pipeline runs, in front of the generated handlers, everything a documented operation needs.
type pipeline struct {
	opts *Options
	ops  *operations
	mux  *chi.Mux
	errs *errorWriter
}

// served reports whether the route of method and pattern is offered: documented, or outside the
// generated routes.
func (p *pipeline) served(method, pattern string) bool {
	_, documented := p.ops.byRoute[routeKey(method, pattern)]

	return documented || !strings.HasPrefix(pattern, _apiPrefix)
}

func (p *pipeline) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pattern, params, allowed := lookup(p.mux, r.Method, r.URL.Path, p.served)

	switch {
	case pattern == "" && len(allowed) > 0:
		p.errs.write(w, r, methodNotAllowed(w, allowed))

		return
	case pattern == "":
		p.errs.write(w, r, notFound())

		return
	}

	setRoute(r.Context(), r.Method, pattern)

	op, documented := p.ops.byRoute[routeKey(r.Method, pattern)]
	if !documented {
		p.mux.ServeHTTP(w, r)

		return
	}

	ctx := reqctx.WithOperation(r.Context(), op.id)
	ctx = reqctx.WithClient(ctx, &reqctx.Client{IP: p.opts.TrustedProxies.ClientIP(r), UserAgent: r.UserAgent()})
	r = r.WithContext(context.WithValue(ctx, paramsKey{}, params))

	r, err := p.prepare(w, r, op, params)
	if err != nil {
		p.errs.write(w, r, err)

		return
	}

	p.execute(w, r, op)
}

// prepare runs the checks that precede the handler and returns the request to hand over.
func (p *pipeline) prepare(w http.ResponseWriter, r *http.Request, op *operation, params map[string]string) (*http.Request, error) {
	limits := newLimitHeaders(w)
	defer limits.write()

	if err := p.limitIP(r, limits); err != nil {
		return r, err
	}

	r, err := p.authenticate(r, &op.policy)
	if err != nil {
		return r, err
	}

	if err := p.limitPolicy(r, &op.policy, limits); err != nil {
		return r, err
	}

	if err := p.authorize(r, &op.policy); err != nil {
		return r, err
	}

	if r, err = p.readBody(r, op); err != nil {
		return r, err
	}

	return r, p.validate(r, op, params)
}

// execute runs the handler under the idempotency key and the transaction of the operation.
func (p *pipeline) execute(w http.ResponseWriter, r *http.Request, op *operation) {
	handler := http.Handler(p.mux)
	handler = p.audited(op, handler)
	handler = p.transactional(op, handler)
	handler = p.idempotent(op, handler)

	handler.ServeHTTP(w, r)
}

type paramsKey struct{}

// pathParam returns a path parameter of the matched route.
func pathParam(ctx context.Context, name string) string {
	params, _ := ctx.Value(paramsKey{}).(map[string]string)

	return params[name]
}

// bearer returns the token of an Authorization: Bearer header and whether the header was sent.
func bearer(r *http.Request) (token string, present bool) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", false
	}

	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}

	return strings.TrimSpace(token), true
}

// isServiceUnavailable reports an error answered with 503.
func isServiceUnavailable(err error) bool {
	return errors.Is(err, entity.New(entity.KeyServiceUnavailable))
}
