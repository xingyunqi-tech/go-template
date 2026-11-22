package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
)

const (
	_requestIDHeader = "X-Request-Id"
	_corsMaxAge      = "600"
)

const (
	_corsAllowHeaders  = "Authorization, Content-Type, Accept-Language, X-Request-Id, Idempotency-Key, traceparent, tracestate"
	_corsExposeHeaders = "X-Request-Id, Retry-After, X-RateLimit-Limit, X-RateLimit-Remaining, X-RateLimit-Reset"
	_corsAllowMethods  = "GET, HEAD, POST, PUT, PATCH, DELETE"
)

var (
	_requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

	errPanic = errors.New("panic")
)

// exchange collects what the access log reports about a request.
type exchange struct {
	// request is the request otelhttp passed down; its Pattern becomes http.route.
	request *http.Request
	route   string
	userID  string
	err     error
}

type exchangeKey struct{}

func exchangeOf(ctx context.Context) *exchange {
	ex, _ := ctx.Value(exchangeKey{}).(*exchange)

	return ex
}

// setRoute records the matched route for the access log and for otelhttp.
func setRoute(ctx context.Context, method, pattern string) {
	if ex := exchangeOf(ctx); ex != nil {
		ex.route = pattern
		ex.request.Pattern = method + " " + pattern
	}
}

func setUserID(ctx context.Context, id string) {
	if ex := exchangeOf(ctx); ex != nil {
		ex.userID = id
	}
}

func setError(ctx context.Context, err error) {
	if ex := exchangeOf(ctx); ex != nil {
		ex.err = err
	}
}

// capturePattern keeps the request otelhttp handed over so that setRoute can set its Pattern.
func capturePattern(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ex := &exchange{request: r, route: "", userID: "", err: nil}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), exchangeKey{}, ex)))
	})
}

// requestID keeps a valid inbound X-Request-Id or generates a UUIDv7, and echoes it.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(_requestIDHeader)
		if !_requestIDPattern.MatchString(id) {
			id = uuid.NewV7().String()
		}

		w.Header().Set(_requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(reqctx.WithRequestID(r.Context(), id)))
	})
}

// statusRecorder remembers the status of the response.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}

	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}

	n, err := s.ResponseWriter.Write(b)
	if err != nil {
		return n, fmt.Errorf("write response: %w", err)
	}

	return n, nil
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// accessLog writes one record per request and turns a panic raised before the response started
// into a 500.
func accessLog(log *slog.Logger, errs *errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 0}

			defer func() { //nolint:contextcheck // the deferred call reads the context from r
				if p := recover(); p != nil {
					if p == http.ErrAbortHandler || rec.status != 0 { //nolint:errorlint,err113 // net/http compares the sentinel by identity
						panic(p)
					}

					log.ErrorContext(r.Context(), "panic", "error", fmt.Sprint(p), "stack", string(debug.Stack()))
					errs.write(rec, r, fmt.Errorf("%w: %v", errPanic, p))
				}

				logRequest(log, r, rec.status, time.Since(start))
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

func logRequest(log *slog.Logger, r *http.Request, status int, elapsed time.Duration) {
	ex := exchangeOf(r.Context())

	level := slog.LevelInfo

	switch {
	case status >= http.StatusInternalServerError:
		level = slog.LevelError
	case status >= http.StatusBadRequest:
		level = slog.LevelWarn
	}

	attrs := []slog.Attr{
		slog.String("method", r.Method),
		slog.String("route", ex.route),
		slog.Int("status", status),
		slog.Float64("duration_ms", float64(elapsed.Microseconds())/float64(time.Millisecond/time.Microsecond)),
		slog.String("user_id", ex.userID),
	}

	if ex.err != nil && status >= http.StatusInternalServerError {
		attrs = append(attrs, slog.String("error", ex.err.Error()))
	}

	log.LogAttrs(r.Context(), level, "request", attrs...)
}

// securityHeaders are sent with every response; handlers may override them.
func securityHeaders() [][2]string {
	return [][2]string{
		{"Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'"},
		{"Cross-Origin-Resource-Policy", "same-site"},
		{"Referrer-Policy", "no-referrer"},
		{"Strict-Transport-Security", "max-age=31536000; includeSubDomains"},
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
	}
}

func secureHeaders(next http.Handler) http.Handler {
	headers := securityHeaders()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for _, kv := range headers {
			h.Set(kv[0], kv[1])
		}

		next.ServeHTTP(w, r)
	})
}

// cors allows the whitelisted origins, or every origin with any; preflights are answered with
// 204 before routing.
func cors(origins []string, anyOrigin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			origin := r.Header.Get("Origin")
			allowed := origin != "" && (anyOrigin || slices.Contains(origins, origin))

			h.Add("Vary", "Origin")

			if allowed {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Expose-Headers", _corsExposeHeaders)
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")

				if allowed {
					h.Set("Access-Control-Allow-Methods", _corsAllowMethods)
					h.Set("Access-Control-Allow-Headers", _corsAllowHeaders)
					h.Set("Access-Control-Max-Age", _corsMaxAge)
				}

				w.WriteHeader(http.StatusNoContent)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// withLanguage resolves the request language from Accept-Language.
func withLanguage(translator *i18n.Translator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			lang := translator.Resolve(strings.Join(r.Header.Values("Accept-Language"), ", "))
			next.ServeHTTP(w, r.WithContext(i18n.WithLanguage(r.Context(), lang)))
		})
	}
}

// methodNotAllowed is the error of a path whose routes all have other methods.
func methodNotAllowed(w http.ResponseWriter, allowed []string) error {
	w.Header().Set("Allow", strings.Join(allowed, ", "))

	return entity.New(entity.KeyMethodNotAllowed)
}
