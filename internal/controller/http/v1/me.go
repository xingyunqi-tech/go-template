package v1

import (
	"context"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
)

func userEnvelope(u *entity.User) gen.UserEnvelope {
	return gen.UserEnvelope{Code: response.Code, Message: response.Message, Data: userOf(u)}
}

// GetMe handles GET /api/v1/me.
func (s *Server) GetMe(ctx context.Context, _ gen.GetMeRequestObject) (gen.GetMeResponseObject, error) {
	u, err := s.users.Me(ctx, caller(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.GetMe200JSONResponse(userEnvelope(u)), nil
}

// UpdateMe handles PATCH /api/v1/me.
func (s *Server) UpdateMe(ctx context.Context, req gen.UpdateMeRequestObject) (gen.UpdateMeResponseObject, error) {
	u, err := s.users.UpdateMe(ctx, caller(ctx), req.Body.DisplayName, req.Body.AvatarFileId)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.UpdateMe200JSONResponse(userEnvelope(u)), nil
}

// ChangePassword handles PUT /api/v1/me/password.
func (s *Server) ChangePassword(ctx context.Context, req gen.ChangePasswordRequestObject) (gen.ChangePasswordResponseObject, error) {
	c := caller(ctx)

	if err := s.users.ChangePassword(ctx, c, &usecase.PasswordChange{Current: req.Body.CurrentPassword, New: req.Body.NewPassword}); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	reqctx.SetAuditTarget(ctx, c.UserID, uuid.Nil())

	return gen.ChangePassword200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// ListMySessions handles GET /api/v1/me/sessions.
func (s *Server) ListMySessions(ctx context.Context, _ gen.ListMySessionsRequestObject) (gen.ListMySessionsResponseObject, error) {
	views, err := s.sessions.List(ctx, caller(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	items := make([]gen.Session, 0, len(views))
	for i := range views {
		items = append(items, sessionOf(&views[i]))
	}

	return gen.ListMySessions200JSONResponse{Code: response.Code, Message: response.Message, Data: items}, nil
}

// RevokeMySession handles DELETE /api/v1/me/sessions/{id}.
func (s *Server) RevokeMySession(ctx context.Context, req gen.RevokeMySessionRequestObject) (gen.RevokeMySessionResponseObject, error) {
	if err := s.sessions.Revoke(ctx, caller(ctx), req.Id); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.RevokeMySession200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// RevokeOtherSessions handles POST /api/v1/me/sessions/revoke-others.
func (s *Server) RevokeOtherSessions(ctx context.Context, _ gen.RevokeOtherSessionsRequestObject) (gen.RevokeOtherSessionsResponseObject, error) {
	if err := s.sessions.RevokeOthers(ctx, caller(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.RevokeOtherSessions200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}
