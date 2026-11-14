// Package logger builds the process-wide slog.Logger.
package logger

import (
	"context"
	"io"
	"log/slog"
)

// Levels outside slog's four built-in ones, matching pino's names.
const (
	LevelTrace slog.Level = slog.LevelDebug - 4
	LevelFatal slog.Level = slog.LevelError + 4
)

// ContextAttrs returns the attributes a record logged with ctx carries, such as request IDs.
type ContextAttrs func(ctx context.Context) []slog.Attr

// New returns a JSON logger, or a single-line text logger when pretty is set; level "silent"
// discards. Each record gets the attributes of every extract function.
func New(w io.Writer, level string, pretty bool, extract ...ContextAttrs) *slog.Logger {
	if level == "silent" {
		return slog.New(slog.DiscardHandler)
	}

	opts := &slog.HandlerOptions{Level: ParseLevel(level)} //nolint:exhaustruct // defaults

	var h slog.Handler = slog.NewJSONHandler(w, opts)
	if pretty {
		h = slog.NewTextHandler(w, opts)
	}

	if len(extract) == 0 {
		return slog.New(h)
	}

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
	case "trace":
		return LevelTrace
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "fatal":
		return LevelFatal
	default:
		return slog.LevelInfo
	}
}
