package v1

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
)

// healthResult is terminus HealthCheckService.check: any indicator down is a 503.
func (s *Server) healthResult(ctx context.Context, r health.Result) (view.Health, error) {
	v := view.NewHealth(&r)
	if r.OK() {
		return v, nil
	}

	failed, _ := json.Marshal(v.Error)
	s.log.ErrorContext(ctx, "health check has failed", "error", string(failed))

	return v, httperr.Status(http.StatusServiceUnavailable)
}

// HealthCheck handles GET /api/v1/health.
func (s *Server) HealthCheck(ctx context.Context, _ gen.HealthCheckRequestObject) (gen.HealthCheckResponseObject, error) {
	v, err := s.healthResult(ctx, s.health.Full(ctx))
	if err != nil {
		return nil, err
	}

	return gen.HealthCheck200JSONResponse(v), nil
}

// LivenessProbe handles GET /api/v1/health/live.
func (s *Server) LivenessProbe(ctx context.Context, _ gen.LivenessProbeRequestObject) (gen.LivenessProbeResponseObject, error) {
	v, err := s.healthResult(ctx, s.health.Live(ctx))
	if err != nil {
		return nil, err
	}

	return gen.LivenessProbe200JSONResponse(v), nil
}

// ReadinessProbe handles GET /api/v1/health/ready.
func (s *Server) ReadinessProbe(ctx context.Context, _ gen.ReadinessProbeRequestObject) (gen.ReadinessProbeResponseObject, error) {
	v, err := s.healthResult(ctx, s.health.Ready(ctx))
	if err != nil {
		return nil, err
	}

	return gen.ReadinessProbe200JSONResponse(v), nil
}
