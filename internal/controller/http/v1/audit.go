package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// ListAuditLogs handles GET /api/v1/audit-logs.
func (s *Server) ListAuditLogs(ctx context.Context, req gen.ListAuditLogsRequestObject) (gen.ListAuditLogsResponseObject, error) {
	q := &usecase.AuditQuery{ActorSub: req.Params.ActorSub, Action: req.Params.Action, Cursor: "", Limit: pagination.DefaultSize}

	if req.Params.Cursor != nil {
		q.Cursor = *req.Params.Cursor
	}

	if req.Params.Limit != nil {
		q.Limit = *req.Params.Limit
	}

	page, err := s.audits.List(ctx, q)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	items := make([]gen.AuditLog, 0, len(page.Items))

	for i := range page.Items {
		e := &page.Items[i]

		var data *map[string]any
		if e.Data != nil {
			data = &e.Data
		}

		items = append(items, gen.AuditLog{
			Id: e.ID, ActorSub: e.ActorSub, Action: e.Action, ResourceType: e.ResourceType, ResourceId: e.ResourceID,
			RequestId: e.RequestID, Ip: e.IP, Data: data, CreatedAt: jsontime.New(e.CreatedAt),
		})
	}

	return gen.ListAuditLogs200JSONResponse{
		Code: response.Code, Message: response.Message, Data: gen.AuditLogPage{Items: items, NextCursor: page.NextCursor},
	}, nil
}
