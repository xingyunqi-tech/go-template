package http

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_requestIDHeader     = "X-Request-Id"
	_jsonBodyLimit       = 256 * 1024
	_jsonWhitespace      = " \t\n\r"
	_byteOrderMark       = "\ufeff"
	_decompressionFailed = "Decompression failed"
	_corsDefaultMethod   = "GET,HEAD,PUT,PATCH,POST,DELETE"
	_corsProdMethods     = "GET,POST,PUT,PATCH,DELETE,OPTIONS"
	_corsProdHeaders     = "Content-Type,Authorization,Accept-Language"
	_corsExposed         = "X-Request-Id,Retry-After,X-RateLimit-Limit,X-RateLimit-Remaining,X-RateLimit-Reset"
	_entityTooLarge      = "request entity too large"
	_v8ContextMinLen     = 21
	_v8ContextChars      = 10
)

var _requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

// securityHeaders are the headers nestjs helmet sends, in the same order and with the same values.
func securityHeaders() [][2]string {
	return [][2]string{
		{
			"Content-Security-Policy",
			"default-src 'self';base-uri 'self';font-src 'self' https: data:;form-action 'self';" +
				"frame-ancestors 'self';img-src 'self' data: https:;object-src 'none';script-src 'self';" +
				"script-src-attr 'none';style-src 'self' 'unsafe-inline';upgrade-insecure-requests",
		},
		{"Cross-Origin-Resource-Policy", "cross-origin"},
		{"Origin-Agent-Cluster", "?1"},
		{"Referrer-Policy", "no-referrer"},
		{"Strict-Transport-Security", "max-age=31536000; includeSubDomains"},
		{"X-Content-Type-Options", "nosniff"},
		{"X-DNS-Prefetch-Control", "off"},
		{"X-Download-Options", "noopen"},
		{"X-Frame-Options", "SAMEORIGIN"},
		{"X-Permitted-Cross-Domain-Policies", "none"},
		{"X-XSS-Protection", "0"},
	}
}

// errorWriter renders errors as the translated nestjs error body and logs them.
type errorWriter struct {
	translator *i18n.Translator
	log        *slog.Logger
	report     func(ctx context.Context, err error)
}

func (e *errorWriter) write(w http.ResponseWriter, r *http.Request, err error) {
	ctx := r.Context()
	lang := i18n.Language(ctx)

	setError(ctx, err)

	body := httperr.Build(err, func(key string) (string, bool) {
		return e.translator.Translate(lang, key)
	})

	attrs := []any{"path", r.URL.Path, "status", body.Status, "cause", body.Cause}

	switch {
	case body.Status >= http.StatusInternalServerError:
		e.log.ErrorContext(ctx, "request failed", append(attrs, "error", err.Error())...)

		if e.report != nil {
			e.report(ctx, err)
		}
	case body.Status >= http.StatusBadRequest:
		e.log.WarnContext(ctx, "request rejected", attrs...)
	}

	if writeErr := httperr.Write(w, body); writeErr != nil {
		e.log.WarnContext(ctx, "write error response", "error", writeErr.Error())
	}
}

// requestContext keeps a single valid inbound X-Request-Id or generates a UUID, echoes it, and
// resolves the request language from Accept-Language.
func requestContext(translator *i18n.Translator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			incoming := r.Header.Values(_requestIDHeader)

			id := ""
			if len(incoming) == 1 && _requestIDPattern.MatchString(incoming[0]) {
				id = incoming[0]
			} else {
				id = uuid.NewV4().String()
				r.Header.Set(_requestIDHeader, id)
			}

			w.Header().Set(_requestIDHeader, id)

			lang := translator.Resolve(strings.Join(r.Header.Values("Accept-Language"), ", "))
			ctx := i18n.WithLanguage(reqctx.WithRequestID(r.Context(), id), lang)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// corsOptions follows cors@2.8: nil origins reflect the request origin and headers.
type corsOptions struct {
	origins        []string
	methods        string
	allowedHeaders string
}

func newCORSOptions(production bool, frontendDomain string) corsOptions {
	if !production || strings.TrimSpace(frontendDomain) == "" {
		return corsOptions{origins: nil, methods: _corsDefaultMethod, allowedHeaders: ""}
	}

	var origins []string
	for o := range strings.SplitSeq(frontendDomain, ",") {
		origins = append(origins, strings.TrimSpace(o))
	}

	return corsOptions{origins: origins, methods: _corsProdMethods, allowedHeaders: _corsProdHeaders}
}

func (c corsOptions) applyOrigin(h http.Header, origin string) {
	allowed := c.origins == nil
	if !allowed {
		for _, o := range c.origins {
			if o == origin {
				allowed = true

				break
			}
		}
	}

	if allowed && origin != "" {
		h.Set("Access-Control-Allow-Origin", origin)
	}

	vary(h, "Origin")
}

// cors answers every OPTIONS request with 204 before routing and decorates other responses.
func cors(opts corsOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			h := w.Header()

			if r.Method == http.MethodOptions {
				opts.applyOrigin(h, origin)
				h.Set("Access-Control-Allow-Credentials", "true")
				h.Set("Access-Control-Allow-Methods", opts.methods)

				allowed := opts.allowedHeaders

				if allowed == "" {
					allowed = r.Header.Get("Access-Control-Request-Headers")

					vary(h, "Access-Control-Request-Headers")
				}

				if allowed != "" {
					h.Set("Access-Control-Allow-Headers", allowed)
				}

				h.Set("Access-Control-Expose-Headers", _corsExposed)
				h.Set("Content-Length", "0")
				w.WriteHeader(http.StatusNoContent)

				return
			}

			opts.applyOrigin(h, origin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Set("Access-Control-Expose-Headers", _corsExposed)

			next.ServeHTTP(w, r)
		})
	}
}

// vary appends field to the Vary header like the vary npm package.
func vary(h http.Header, field string) {
	current := h.Get("Vary")
	if current == "" {
		h.Set("Vary", field)

		return
	}

	if strings.TrimSpace(current) == "*" {
		return
	}

	existing := map[string]bool{}
	for v := range strings.SplitSeq(current, ",") {
		existing[strings.ToLower(strings.TrimSpace(v))] = true
	}

	additions := []string{current}

	for f := range strings.SplitSeq(field, ",") {
		f = strings.TrimSpace(f)
		if f == "*" {
			h.Set("Vary", "*")

			return
		}

		if !existing[strings.ToLower(f)] {
			additions = append(additions, f)
		}
	}

	h.Set("Vary", strings.Join(additions, ", "))
}

// secureHeaders sets the helmet headers; handlers may override them.
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

// statusRecorder remembers whether the response has started.
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

var errPanic = errors.New("panic")

// recovered writes a 500 for a panic raised before the response started; other panics continue.
func (e *errorWriter) recovered(rec *statusRecorder, r *http.Request, p any) {
	if p == http.ErrAbortHandler || rec.status != 0 { //nolint:errorlint,err113 // net/http compares the sentinel by identity
		panic(p)
	}

	e.write(rec, r, fmt.Errorf("%w: %v", errPanic, p))
}

// accessLog writes the pino-http request log and turns panics that happen before the response
// starts into a 500 error body.
func accessLog(log *slog.Logger, errs *errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 0}
			panicked := false

			defer func() {
				if p := recover(); p != nil {
					panicked = true

					errs.recovered(rec, r, p)
				}

				logRequest(log, r, rec.status, start, panicked)
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

const (
	_redacted        = "[REDACTED]"
	_millisPerSecond = 1000
)

func requestLevel(status int, panicked bool) slog.Level {
	switch {
	case status >= http.StatusInternalServerError || panicked:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func logRequest(log *slog.Logger, r *http.Request, status int, start time.Time, panicked bool) {
	ctx := r.Context()

	if status == 0 {
		status = http.StatusInternalServerError
	}

	message := r.Method + " " + r.RequestURI + " " + strconv.Itoa(status)

	ex := exchangeOf(ctx)
	if ex != nil && ex.err != nil && (panicked || status >= http.StatusInternalServerError) {
		message += " - " + ex.err.Error()
	}

	args := []any{
		"context", "HTTP",
		"req",
		map[string]any{
			"id": reqctx.RequestID(ctx), "method": r.Method, "url": r.RequestURI,
			"query": logQuery(r.URL.RawQuery), "headers": logHeaders(r.Header), "remoteAddress": remoteAddress(r.RemoteAddr),
		},
		"res",
		map[string]any{"statusCode": status},
		"responseTime", math.Round(float64(time.Since(start).Microseconds()) / _millisPerSecond),
	}

	if ex != nil && ex.userID != "" {
		args = append(args, "user_id", ex.userID)
	}

	log.Log(ctx, requestLevel(status, panicked), message, args...) //nolint:sloglint // pino-http field names
}

func logHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))

	for k, v := range h {
		name := strings.ToLower(k)
		if name == "authorization" || name == "cookie" {
			out[name] = _redacted

			continue
		}

		out[name] = strings.Join(v, ", ")
	}

	return out
}

func logQuery(raw string) map[string]string {
	out := map[string]string{}

	values, _ := url.ParseQuery(raw)
	for k, v := range values {
		switch {
		case k == "hash" || k == "token":
			out[k] = _redacted
		case len(v) > 0:
			out[k] = v[len(v)-1]
		}
	}

	return out
}

func remoteAddress(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}

	return host
}

func isJSONContentType(contentType string) bool {
	if contentType == "" {
		return false
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType, _, _ = strings.Cut(contentType, ";")
	}

	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}

// jsonBody parses application/json bodies before routing, like Express's global body-parser.
func jsonBody(errs *errorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isJSONContentType(r.Header.Get("Content-Type")) {
				next.ServeHTTP(w, r)

				return
			}

			if err := checkCharset(r.Header.Get("Content-Type")); err != nil {
				errs.write(w, r, err)

				return
			}

			raw, err := readBody(r)
			if err != nil {
				errs.write(w, r, err)

				return
			}

			value, err := parseJSONBody(raw)
			if err != nil {
				errs.write(w, r, err)

				return
			}

			r.Body = io.NopCloser(bytes.NewReader(raw))
			next.ServeHTTP(w, r.WithContext(reqctx.WithBody(r.Context(), value)))
		})
	}
}

// checkCharset follows body-parser json: only utf-* charsets are accepted.
func checkCharset(contentType string) error {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil //nolint:nilerr // body-parser falls back to utf-8 for unparsable parameters
	}

	charset := strings.ToLower(params["charset"])
	if charset == "" || strings.HasPrefix(charset, "utf-") {
		return nil
	}

	return httperr.New(http.StatusUnsupportedMediaType, `unsupported charset "`+strings.ToUpper(charset)+`"`)
}

// decodedBody follows body-parser's Content-Encoding handling; the size limit applies to the
// decoded body.
func decodedBody(r *http.Request) (io.Reader, error) {
	encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))

	switch encoding {
	case "", "identity":
		if r.ContentLength > _jsonBodyLimit {
			return nil, httperr.New(http.StatusRequestEntityTooLarge, _entityTooLarge)
		}

		return r.Body, nil
	case "gzip":
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, httperr.New(http.StatusBadRequest, _decompressionFailed)
		}

		return zr, nil
	case "deflate":
		zr, err := zlib.NewReader(r.Body)
		if err != nil {
			return nil, httperr.New(http.StatusBadRequest, _decompressionFailed)
		}

		return zr, nil
	default:
		return nil, httperr.New(http.StatusUnsupportedMediaType, `unsupported content encoding "`+encoding+`"`)
	}
}

func readBody(r *http.Request) ([]byte, error) {
	body, err := decodedBody(r)
	if err != nil {
		return nil, err
	}

	raw, err := io.ReadAll(io.LimitReader(body, _jsonBodyLimit+1))
	if err != nil {
		if r.Header.Get("Content-Encoding") != "" {
			return nil, httperr.New(http.StatusBadRequest, _decompressionFailed)
		}

		return nil, httperr.New(http.StatusBadRequest, "request aborted")
	}

	if len(raw) > _jsonBodyLimit {
		return nil, httperr.New(http.StatusRequestEntityTooLarge, _entityTooLarge)
	}

	return raw, nil
}

// parseJSONBody follows body-parser json in strict mode: an empty body is {}, and the first
// non-whitespace character must open an object or an array.
func parseJSONBody(raw []byte) (validatorx.Value, error) {
	if len(raw) == 0 {
		return validatorx.NewObject(), nil
	}

	text := strings.TrimPrefix(string(raw), _byteOrderMark)
	stripped := strings.TrimLeft(text, _jsonWhitespace)

	if stripped == "" {
		return nil, httperr.New(http.StatusBadRequest, "Unexpected end of JSON input")
	}

	if stripped[0] != '{' && stripped[0] != '[' {
		token, _ := utf8.DecodeRuneInString(stripped)

		return nil, httperr.New(http.StatusBadRequest, unexpectedToken(token, text))
	}

	value, err := validatorx.ParseJSON([]byte(text))
	if err != nil {
		return nil, httperr.New(http.StatusBadRequest, strings.TrimPrefix(err.Error(), "parse json: "))
	}

	return value, nil
}

// unexpectedToken is V8's JSON.parse message for an unexpected first token: the whole source when
// shorter than 21 characters, else its first 10 characters.
func unexpectedToken(token rune, source string) string {
	runes := []rune(source)
	if len(runes) < _v8ContextMinLen {
		return "Unexpected token '" + string(token) + `', "` + source + `" is not valid JSON`
	}

	return "Unexpected token '" + string(token) + `', "` + string(runes[:_v8ContextChars]) + `"... is not valid JSON`
}
