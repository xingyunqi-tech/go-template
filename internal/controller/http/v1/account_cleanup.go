package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
)

// AdminRetriggerAccountCleanup handles POST admin/users/:id/cleanup.
func (s *Server) AdminRetriggerAccountCleanup(
	ctx context.Context, req gen.AdminRetriggerAccountCleanupRequestObject,
) (gen.AdminRetriggerAccountCleanupResponseObject, error) {
	if err := s.cleanup.Retrigger(ctx, int(req.Id)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.AdminRetriggerAccountCleanup200Response{Body: nil}, nil
}
