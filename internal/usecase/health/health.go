// Package health runs the @nestjs/terminus indicators of the health endpoints.
package health

import (
	"context"
	"errors"
	"math"
	"net/http"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_heapThresholdBytes    = 300 * 1024 * 1024
	_diskThreshold         = 0.9
	_databaseTimeout       = time.Second
	_poolThresholdPercent  = 90
	_externalTimeout       = 5 * time.Second
	_redisTimeout          = 5 * time.Second
	_percent               = 100
	_methodNotAllowed      = http.StatusMethodNotAllowed
	_metricTotal           = "/memory/classes/total:bytes"
	_metricHeapReleased    = "/memory/classes/heap/released:bytes"
	_wechatURL             = "https://api.weixin.qq.com/cgi-bin/getcallbackip"
	_alipayURL             = "https://openapi.alipay.com/gateway.do"
	_databaseTimeoutReason = "timeout of 1000ms exceeded"
	_message               = "message"
)

// Field is one indicator detail.
type Field struct {
	Name  string
	Value any
}

// Indicator is one check result: {Key: {...Fields, status}}.
type Indicator struct {
	Key    string
	Fields []Field
	Up     bool
}

// Result splits the indicators by status, each in check order.
type Result struct {
	Info  []Indicator
	Error []Indicator
}

// OK reports that every indicator is up.
func (r *Result) OK() bool {
	return len(r.Error) == 0
}

// Config holds the optional external probes.
type Config struct {
	WechatAppID     string
	WechatAppSecret string
	AlipayAppID     string
}

// Deps are the probed dependencies; Redis is nil when REDIS_ENABLED is off.
type Deps struct {
	Config   Config
	Database repo.DatabaseHealth
	Redis    repo.Pinger
	HTTP     *http.Client
	DiskPath string
	// Memory returns the bytes held by the runtime; nil reads runtime/metrics.
	Memory func() uint64
	// Disk returns total and available bytes of DiskPath; nil uses statfs.
	Disk func(path string) (total, available uint64, err error)
}

// Service runs the checks.
type Service struct {
	deps     Deps
	draining atomic.Bool
}

// New returns a Service.
func New(deps *Deps) *Service {
	d := *deps

	if d.Memory == nil {
		d.Memory = RuntimeMemory
	}

	if d.Disk == nil {
		d.Disk = diskUsage
	}

	if d.DiskPath == "" {
		d.DiskPath = "/"
	}

	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: _externalTimeout} //nolint:exhaustruct // default transport
	}

	return &Service{deps: d, draining: atomic.Bool{}}
}

// Drain makes readiness fail from now on, while the process shuts down.
func (s *Service) Drain() {
	s.draining.Store(true)
}

type check func(ctx context.Context) Indicator

// Full is GET /api/v1/health.
func (s *Service) Full(ctx context.Context) Result {
	checks := []check{
		s.memoryHeap, s.disk, s.database, s.databasePool,
	}
	if s.deps.Redis != nil {
		checks = append(checks, s.redis)
	}

	return run(ctx, append(checks, s.wechat, s.alipay))
}

// Live is GET /api/v1/health/live.
func (s *Service) Live(ctx context.Context) Result {
	return run(ctx, []check{s.memoryHeap})
}

// Ready is GET /api/v1/health/ready; it fails once the process is draining.
func (s *Service) Ready(ctx context.Context) Result {
	if s.draining.Load() {
		return Result{Info: nil, Error: []Indicator{down("shutdown", Field{Name: _message, Value: "Shutting down"})}}
	}

	return run(ctx, []check{s.database})
}

func run(ctx context.Context, checks []check) Result {
	results := make([]Indicator, len(checks))

	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Go(func() { results[i] = c(ctx) })
	}

	wg.Wait()

	var r Result

	for _, ind := range results {
		if ind.Up {
			r.Info = append(r.Info, ind)
		} else {
			r.Error = append(r.Error, ind)
		}
	}

	return r
}

func up(key string, fields ...Field) Indicator {
	return Indicator{Key: key, Fields: fields, Up: true}
}

func down(key string, fields ...Field) Indicator {
	return Indicator{Key: key, Fields: fields, Up: false}
}

func exceeded(keyword string) Field {
	return Field{Name: _message, Value: "Used " + keyword + " exceeded the set threshold"}
}

func jsRound(v float64) int64 {
	return int64(math.Floor(v + 0.5)) //nolint:mnd // Math.round
}

func elapsedMillis(start time.Time) int64 {
	return jsRound(float64(time.Since(start).Microseconds()) / float64(time.Millisecond/time.Microsecond))
}

// RuntimeMemory is the memory the Go runtime holds and has not returned to the OS.
func RuntimeMemory() uint64 {
	samples := []metrics.Sample{
		{Name: _metricTotal, Value: metrics.Value{}},
		{Name: _metricHeapReleased, Value: metrics.Value{}},
	}
	metrics.Read(samples)

	var total, released uint64
	if samples[0].Value.Kind() == metrics.KindUint64 {
		total = samples[0].Value.Uint64()
	}

	if samples[1].Value.Kind() == metrics.KindUint64 {
		released = samples[1].Value.Uint64()
	}

	return total - released
}

func (s *Service) memoryHeap(context.Context) Indicator {
	if s.deps.Memory() > _heapThresholdBytes {
		return down("memory_heap", exceeded("heap"))
	}

	return up("memory_heap")
}

func (s *Service) disk(context.Context) Indicator {
	total, available, err := s.deps.Disk(s.deps.DiskPath)
	if err != nil || total == 0 || float64(total-available)/float64(total) > _diskThreshold {
		return down("disk", exceeded("disk storage"))
	}

	return up("disk")
}

func (s *Service) database(ctx context.Context) Indicator {
	start := time.Now()

	pingCtx, cancel := context.WithTimeout(ctx, _databaseTimeout)
	defer cancel()

	err := s.deps.Database.Ping(pingCtx)
	elapsed := Field{Name: "responseTime", Value: elapsedMillis(start)}

	switch {
	case err == nil:
		return up("database", elapsed)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(pingCtx.Err(), context.DeadlineExceeded):
		return down("database", Field{Name: _message, Value: _databaseTimeoutReason}, elapsed)
	default:
		return down("database", Field{Name: _message, Value: err.Error()}, elapsed)
	}
}

func (s *Service) databasePool(ctx context.Context) Indicator {
	active, maximum, err := s.deps.Database.ConnectionStats(ctx)
	if err != nil {
		return down("database_pool", Field{Name: _message, Value: err.Error()})
	}

	if maximum <= 0 {
		return down("database_pool", Field{Name: _message, Value: "Unable to retrieve database pool statistics"})
	}

	percent := jsRound(float64(active) / float64(maximum) * _percent)
	fields := []Field{
		{Name: "activeConnections", Value: active},
		{Name: "maxConnections", Value: maximum},
		{Name: "utilizationPercent", Value: percent},
	}

	if percent > _poolThresholdPercent {
		return down("database_pool", fields...)
	}

	return up("database_pool", fields...)
}

func (s *Service) redis(ctx context.Context) Indicator {
	pingCtx, cancel := context.WithTimeout(ctx, _redisTimeout)
	defer cancel()

	if err := s.deps.Redis.Ping(pingCtx); err != nil {
		return down("redis", Field{Name: _message, Value: err.Error()})
	}

	return up("redis")
}

func (s *Service) wechat(ctx context.Context) Indicator {
	if s.deps.Config.WechatAppID == "" || s.deps.Config.WechatAppSecret == "" {
		return up("wechat_api", Field{Name: _message, Value: "WeChat not configured, skipped"})
	}

	return s.probe(ctx, "wechat_api", http.MethodGet, _wechatURL, false)
}

func (s *Service) alipay(ctx context.Context) Indicator {
	if s.deps.Config.AlipayAppID == "" {
		return up("alipay_api", Field{Name: _message, Value: "Alipay not configured, skipped"})
	}

	return s.probe(ctx, "alipay_api", http.MethodHead, _alipayURL, true)
}

// probe is a fetch with a 5s timeout; failures carry the undici error message.
func (s *Service) probe(ctx context.Context, key, method, url string, allow405 bool) Indicator {
	reqCtx, cancel := context.WithTimeout(ctx, _externalTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, url, http.NoBody)
	if err != nil {
		return down(key, Field{Name: _message, Value: "fetch failed"})
	}

	resp, err := s.deps.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || reqCtx.Err() != nil {
			return down(key, Field{Name: _message, Value: "This operation was aborted"})
		}

		return down(key, Field{Name: _message, Value: "fetch failed"})
	}

	_ = resp.Body.Close()

	if (resp.StatusCode >= 200 && resp.StatusCode < 300) || (allow405 && resp.StatusCode == _methodNotAllowed) {
		return up(key, Field{Name: "responseTime", Value: "OK"})
	}

	return down(key, Field{Name: "statusCode", Value: resp.StatusCode})
}
