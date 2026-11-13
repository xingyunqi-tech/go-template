package v1

import (
	"context"
	"math"
	"runtime"
	"runtime/metrics"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

const (
	_bytesPerMB = 1024 * 1024

	_metricHeapObjects  = "/memory/classes/heap/objects:bytes"
	_metricHeapUnused   = "/memory/classes/heap/unused:bytes"
	_metricHeapFree     = "/memory/classes/heap/free:bytes"
	_metricHeapReleased = "/memory/classes/heap/released:bytes"
	_metricTotal        = "/memory/classes/total:bytes"
)

// GetAppInfo handles GET /.
func (s *Server) GetAppInfo(context.Context, gen.GetAppInfoRequestObject) (gen.GetAppInfoResponseObject, error) {
	return gen.GetAppInfo200Response{Body: view.AppInfo{Name: s.appName}}, nil
}

// SimpleHealthCheck handles GET /api/health.
func (s *Server) SimpleHealthCheck(
	context.Context, gen.SimpleHealthCheckRequestObject,
) (gen.SimpleHealthCheckResponseObject, error) {
	now := s.now()

	return gen.SimpleHealthCheck200JSONResponse{
		Status:    "ok",
		Timestamp: jsontime.New(now),
		Uptime:    int64(now.Sub(s.started).Seconds()),
		Memory:    memory(),
		Node:      runtime.Version(),
	}, nil
}

// memory reports heapUsed as live heap objects, heapTotal as heap memory held from the OS, and
// rss as all memory the runtime holds and has not returned to the OS.
func memory() view.Memory {
	samples := []metrics.Sample{
		{Name: _metricHeapObjects, Value: metrics.Value{}},
		{Name: _metricHeapUnused, Value: metrics.Value{}},
		{Name: _metricHeapFree, Value: metrics.Value{}},
		{Name: _metricHeapReleased, Value: metrics.Value{}},
		{Name: _metricTotal, Value: metrics.Value{}},
	}
	metrics.Read(samples)

	v := make(map[string]uint64, len(samples))
	for _, s := range samples {
		if s.Value.Kind() == metrics.KindUint64 {
			v[s.Name] = s.Value.Uint64()
		}
	}

	return view.Memory{
		HeapUsed:  toMB(v[_metricHeapObjects]),
		HeapTotal: toMB(v[_metricHeapObjects] + v[_metricHeapUnused] + v[_metricHeapFree]),
		Rss:       toMB(v[_metricTotal] - v[_metricHeapReleased]),
	}
}

func toMB(b uint64) int64 {
	return int64(math.Round(float64(b) / _bytesPerMB))
}
