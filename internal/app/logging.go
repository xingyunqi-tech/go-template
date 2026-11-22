package app

import (
	"context"
	"fmt"
	"log/slog"

	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
)

// LogAttrs adds the request ID, the trace and span IDs to records logged during a request.
func LogAttrs(ctx context.Context) []slog.Attr {
	var attrs []slog.Attr

	if id := reqctx.RequestID(ctx); id != "" {
		attrs = append(attrs, slog.String("request_id", id))
	}

	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		attrs = append(attrs, slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}

	return attrs
}

// redisLogger sends go-redis internal messages to slog.
type redisLogger struct {
	log *slog.Logger
}

func (l redisLogger) Printf(ctx context.Context, format string, v ...any) {
	l.log.WarnContext(ctx, "redis client", "message", fmt.Sprintf(format, v...))
}

// RouteLibraryLogs sends the internal logs of third-party clients to log.
func RouteLibraryLogs(log *slog.Logger) {
	goredis.SetLogger(redisLogger{log: log})
}
