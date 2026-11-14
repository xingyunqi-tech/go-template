package metrics

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	_sqlComment = regexp.MustCompile(`(?m)^\s*--.*$`)
	_sqlTable   = regexp.MustCompile(`(?i)\b(?:from|into|update|join)\s+("[^"]+"|[A-Za-z_][\w.]*)`)
)

// QueryLabels returns the operation (first keyword) and the first table named in sql.
func QueryLabels(sql string) (operation, table string) {
	sql = strings.TrimSpace(_sqlComment.ReplaceAllString(sql, ""))

	operation, _, _ = strings.Cut(sql, " ")
	operation = strings.ToLower(strings.TrimSpace(operation))

	table = "unknown"
	if m := _sqlTable.FindStringSubmatch(sql); m != nil {
		table = strings.Trim(m[1], `"`)
	}

	return operation, table
}

type queryKey struct{}

type queryStart struct {
	sql   string
	start time.Time
}

// QueryTracer records database_query_duration_seconds.
type QueryTracer struct {
	m *Metrics
}

// QueryTracer returns the pgx tracer of the database histogram.
func (m *Metrics) QueryTracer() *QueryTracer {
	return &QueryTracer{m: m}
}

var _ pgx.QueryTracer = (*QueryTracer)(nil)

// TraceQueryStart implements pgx.QueryTracer.
func (t *QueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryKey{}, queryStart{sql: data.SQL, start: time.Now()})
}

// TraceQueryEnd implements pgx.QueryTracer.
func (t *QueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	q, ok := ctx.Value(queryKey{}).(queryStart)
	if !ok {
		return
	}

	op, table := QueryLabels(q.sql)
	t.m.DatabaseQuery.WithLabelValues(op, table).Observe(time.Since(q.start).Seconds())
}

// Hit counts a cache hit.
func (m *Metrics) Hit(cacheName string) {
	m.CacheHits.WithLabelValues(cacheName).Inc()
}

// Miss counts a cache miss.
func (m *Metrics) Miss(cacheName string) {
	m.CacheMisses.WithLabelValues(cacheName).Inc()
}
