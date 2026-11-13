package http

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

const (
	_baselinePrefix = "api"
	_unmatchedPath  = "/\x00"
)

type routeTemplate struct {
	pattern  string
	segments []string
}

// routeMatch is the template chosen for a request and the raw (percent-encoded) path segments.
type routeMatch struct {
	method   string
	pattern  string
	segments []string
	template []string
}

type (
	routeMatchKey    struct{}
	undecodableParam struct{}
)

// undecodable returns the raw path parameter that failed to decode on a route whose path, but not
// method, matched the request.
func undecodable(r *http.Request) (string, bool) {
	raw, ok := r.Context().Value(undecodableParam{}).(string)

	return raw, ok
}

func matchedRoute(r *http.Request) *routeMatch {
	m, _ := r.Context().Value(routeMatchKey{}).(*routeMatch)

	return m
}

// param returns the raw value of the named path parameter.
func (m *routeMatch) param(name string) string {
	for i, seg := range m.template {
		if seg == "{"+name+"}" {
			return m.segments[i]
		}
	}

	return ""
}

// pathMatcher rewrites request paths to the casing of the route templates registered for the request
// method, so that chi matches the way Express does: case-insensitive static segments, an optional
// trailing slash, and matching on the percent-encoded path.
type pathMatcher struct {
	router    chi.Routes
	prefix    string
	templates map[string][]routeTemplate
}

func newPathMatcher(router chi.Routes, prefix string) (*pathMatcher, error) {
	m := &pathMatcher{router: router, prefix: prefix, templates: map[string][]routeTemplate{}}

	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		m.templates[method] = append(m.templates[method], routeTemplate{pattern: route, segments: splitPath(route)})

		return nil
	})

	return m, err
}

func (m *pathMatcher) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, match := m.normalize(r.Method, r.URL.EscapedPath())

		rctx := chi.NewRouteContext()
		rctx.Routes = m.router
		rctx.RoutePath = path
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)

		if match != nil {
			ctx = context.WithValue(ctx, routeMatchKey{}, match)
		} else if raw, failed := m.undecodableParam(r.URL.EscapedPath()); failed {
			ctx = context.WithValue(ctx, undecodableParam{}, raw)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *pathMatcher) normalize(method, escaped string) (string, *routeMatch) {
	segments := splitPath(escaped)
	templates := m.templates[method]

	if method == http.MethodHead && len(templates) == 0 {
		method = http.MethodGet
		templates = m.templates[method]
	}

	var (
		best      *routeTemplate
		bestScore []bool
	)

	for i := range templates {
		tpl := &templates[i]

		score, ok := m.match(tpl, segments)
		if ok && (best == nil || moreStatic(score, bestScore)) {
			best, bestScore = tpl, score
		}
	}

	if best == nil {
		return _unmatchedPath, nil
	}

	out := make([]string, len(segments))
	for i, seg := range best.segments {
		if isParam(seg) {
			out[i] = segments[i]
		} else {
			out[i] = seg
		}
	}

	return "/" + strings.Join(out, "/"), &routeMatch{
		method: method, pattern: best.pattern, segments: segments, template: best.segments,
	}
}

// undecodableParam reports a path parameter that does not decode on any route whose path matches,
// whatever its method: Express decodes parameters while matching paths, before checking methods.
func (m *pathMatcher) undecodableParam(escaped string) (string, bool) {
	segments := splitPath(escaped)

	for _, templates := range m.templates {
		for i := range templates {
			tpl := &templates[i]
			if _, ok := m.match(tpl, segments); !ok {
				continue
			}

			if raw, failed := firstUndecodable(tpl, segments); failed {
				return raw, true
			}
		}
	}

	return "", false
}

func firstUndecodable(tpl *routeTemplate, segments []string) (string, bool) {
	for i, seg := range tpl.segments {
		if !isParam(seg) {
			continue
		}

		if _, err := decodeURIComponent(segments[i]); err != nil {
			return segments[i], true
		}
	}

	return "", false
}

func (m *pathMatcher) match(tpl *routeTemplate, segments []string) ([]bool, bool) {
	if len(tpl.segments) != len(segments) {
		return nil, false
	}

	score := make([]bool, len(segments))

	for i, seg := range tpl.segments {
		if isParam(seg) {
			if segments[i] == "" {
				return nil, false
			}

			continue
		}

		if !strings.EqualFold(segments[i], m.literal(tpl, i)) {
			return nil, false
		}

		score[i] = true
	}

	return score, true
}

// literal is the expected request text of a static template segment; the leading "api" of a
// prefixed route is the configured API_PREFIX.
func (m *pathMatcher) literal(tpl *routeTemplate, i int) string {
	if i == 0 && len(tpl.segments) > 1 && tpl.segments[0] == _baselinePrefix {
		return m.prefix
	}

	return tpl.segments[i]
}

func moreStatic(a, b []bool) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i]
		}
	}

	return false
}

func isParam(segment string) bool {
	return strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

func splitPath(path string) []string {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	if path == "" {
		return nil
	}

	return strings.Split(path, "/")
}
