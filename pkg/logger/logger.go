// Package logger builds the process-wide slog.Logger.
package logger

import (
	"io"
	"log/slog"
)

// Levels outside slog's four built-in ones, matching pino's names.
const (
	LevelTrace slog.Level = slog.LevelDebug - 4
	LevelFatal slog.Level = slog.LevelError + 4
)

// New returns a JSON logger, or a single-line text logger when pretty is set; level "silent" discards.
func New(w io.Writer, level string, pretty bool) *slog.Logger {
	if level == "silent" {
		return slog.New(slog.DiscardHandler)
	}

	opts := &slog.HandlerOptions{Level: ParseLevel(level)}
	if pretty {
		return slog.New(slog.NewTextHandler(w, opts))
	}

	return slog.New(slog.NewJSONHandler(w, opts))
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
