// Package logger builds the process-wide slog.Logger with the platform log fields: ts, level,
// msg, service, env, and the request attributes added by the context extractors. Credentials are
// dropped and email addresses and phone numbers masked.
package logger

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	_tsLayout       = "2006-01-02T15:04:05.000Z"
	_phoneSuffix    = 4
	_phoneMasked    = 8
	_phoneMinLength = 10
)

// Options configures New.
type Options struct {
	// Level is LOG_LEVEL: debug, info, warn or error.
	Level string
	// Text selects the single-line text format instead of JSON lines.
	Text    bool
	Service string
	Env     string
}

// ContextAttrs returns the attributes a record logged with ctx carries, such as request IDs.
type ContextAttrs func(ctx context.Context) []slog.Attr

// New returns a logger writing to w; every record gets service, env and the attributes of each
// extract function.
func New(w io.Writer, opts Options, extract ...ContextAttrs) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: ParseLevel(opts.Level), ReplaceAttr: ReplaceAttr} //nolint:exhaustruct // defaults

	var h slog.Handler = slog.NewJSONHandler(w, handlerOpts)
	if opts.Text {
		h = slog.NewTextHandler(w, handlerOpts)
	}

	h = h.WithAttrs([]slog.Attr{slog.String("service", opts.Service), slog.String("env", opts.Env)})

	return slog.New(&contextHandler{Handler: h, extract: extract})
}

type contextHandler struct {
	slog.Handler

	extract []ContextAttrs
}

func (h *contextHandler) Handle(ctx context.Context, r slog.Record) error { //nolint:gocritic // slog.Handler signature
	if ctx != nil {
		for _, fn := range h.extract {
			r.AddAttrs(fn(ctx)...)
		}
	}

	return h.Handler.Handle(ctx, r) //nolint:wrapcheck // handler pass-through
}

func (h *contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithAttrs(attrs), extract: h.extract}
}

func (h *contextHandler) WithGroup(name string) slog.Handler {
	return &contextHandler{Handler: h.Handler.WithGroup(name), extract: h.extract}
}

// ParseLevel maps a LOG_LEVEL value to a slog level; unknown values are info.
func ParseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ReplaceAttr renames time to ts in UTC with milliseconds, writes levels in lower case, drops
// credential attributes and masks email and phone attributes.
func ReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 {
		if replaced, ok := builtin(a); ok {
			return replaced
		}
	}

	key := strings.ToLower(a.Key)

	switch {
	case redacted(key):
		return slog.Attr{}
	case strings.Contains(key, "email") && a.Value.Kind() == slog.KindString:
		return slog.String(a.Key, MaskEmail(a.Value.String()))
	case strings.Contains(key, "phone") && a.Value.Kind() == slog.KindString:
		return slog.String(a.Key, MaskPhone(a.Value.String()))
	default:
		return a
	}
}

// builtin rewrites the time and level attributes.
func builtin(a slog.Attr) (slog.Attr, bool) {
	switch a.Key {
	case slog.TimeKey:
		if t, ok := a.Value.Any().(time.Time); ok {
			return slog.String("ts", t.UTC().Format(_tsLayout)), true
		}
	case slog.LevelKey:
		if l, ok := a.Value.Any().(slog.Level); ok {
			return slog.String(slog.LevelKey, strings.ToLower(l.String())), true
		}
	}

	return a, false
}

func redacted(key string) bool {
	key = strings.NewReplacer("_", "", "-", "").Replace(key)

	switch key {
	case "authorization", "cookie", "setcookie", "code", "secret", "signature", "otp":
		return true
	}

	return strings.Contains(key, "password") || strings.Contains(key, "token")
}

// MaskEmail keeps the first character, not byte, of the local part: a***@example.com.
func MaskEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return "***"
	}

	first, _ := utf8.DecodeRuneInString(local)

	return string(first) + "***@" + domain
}

// MaskPhone hides the eight characters before the last four: +86138****0000.
func MaskPhone(phone string) string {
	if len(phone) < _phoneMinLength {
		return "***"
	}

	return phone[:len(phone)-_phoneMasked] + "****" + phone[len(phone)-_phoneSuffix:]
}
