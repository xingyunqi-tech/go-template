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
	segments []string
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
		m.templates[method] = append(m.templates[method], routeTemplate{segments: splitPath(route)})

		return nil
	})

	return m, err
}

func (m *pathMatcher) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.NewRouteContext()
		rctx.Routes = m.router
		rctx.RoutePath = m.normalize(r.Method, r.URL.EscapedPath())
		ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *pathMatcher) normalize(method, escaped string) string {
	segments := splitPath(escaped)
	templates := m.templates[method]

	if method == http.MethodHead && len(templates) == 0 {
		templates = m.templates[http.MethodGet]
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
		return _unmatchedPath
	}

	out := make([]string, len(segments))
	for i, seg := range best.segments {
		if isParam(seg) {
			out[i] = segments[i]
		} else {
			out[i] = seg
		}
	}

	return "/" + strings.Join(out, "/")
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
