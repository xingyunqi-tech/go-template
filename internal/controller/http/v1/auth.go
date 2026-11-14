package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
)

func (s *Server) login(l *auth.Login) view.Login {
	var created *view.Created
	if l.Created != nil {
		created = &view.Created{RoleID: l.Created.RoleID, StatusID: l.Created.StatusID, Photo: l.Created.Photo}
	}

	return view.Login{
		RefreshToken: l.Tokens.RefreshToken, Token: l.Tokens.Token, TokenExpires: l.Tokens.TokenExpires,
		User: view.NewUser(l.User, s.urls, created),
	}
}

// LoginWithEmail handles POST auth/email/login.
func (s *Server) LoginWithEmail(ctx context.Context, req gen.LoginWithEmailRequestObject) (gen.LoginWithEmailResponseObject, error) {
	l, err := s.auth.LoginWithEmail(ctx, req.Body.Email, req.Body.Password, reqctx.Client(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.LoginWithEmail200JSONResponse(s.login(l)), nil
}

// RegisterWithEmail handles POST auth/email/register.
func (s *Server) RegisterWithEmail(ctx context.Context, _ gen.RegisterWithEmailRequestObject) (gen.RegisterWithEmailResponseObject, error) {
	in, err := userInput(body(ctx))
	if err != nil {
		return nil, err
	}

	if err := s.auth.Register(ctx, in); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.RegisterWithEmail200Response{Body: nil}, nil
}

// ConfirmEmail handles POST auth/email/confirm.
func (s *Server) ConfirmEmail(ctx context.Context, req gen.ConfirmEmailRequestObject) (gen.ConfirmEmailResponseObject, error) {
	if err := s.auth.ConfirmEmail(ctx, req.Body.Hash); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.ConfirmEmail200Response{Body: nil}, nil
}

// ConfirmNewEmail handles POST auth/email/confirm/new.
func (s *Server) ConfirmNewEmail(ctx context.Context, req gen.ConfirmNewEmailRequestObject) (gen.ConfirmNewEmailResponseObject, error) {
	if err := s.auth.ConfirmNewEmail(ctx, req.Body.Hash); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.ConfirmNewEmail200Response{Body: nil}, nil
}

// ForgotPassword handles POST auth/forgot/password.
func (s *Server) ForgotPassword(ctx context.Context, req gen.ForgotPasswordRequestObject) (gen.ForgotPasswordResponseObject, error) {
	if err := s.auth.ForgotPassword(ctx, req.Body.Email); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.ForgotPassword200Response{Body: nil}, nil
}

// ResetPassword handles POST auth/reset/password.
func (s *Server) ResetPassword(ctx context.Context, req gen.ResetPasswordRequestObject) (gen.ResetPasswordResponseObject, error) {
	if err := s.auth.ResetPassword(ctx, req.Body.Hash, req.Body.Password); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.ResetPassword200Response{Body: nil}, nil
}

// GetCurrentUser handles GET auth/me.
func (s *Server) GetCurrentUser(ctx context.Context, _ gen.GetCurrentUserRequestObject) (gen.GetCurrentUserResponseObject, error) {
	u, err := s.auth.Me(ctx, reqctx.Principal(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetCurrentUser200JSONResponse(view.NewUser(u, s.urls, nil)), nil
}

// UpdateCurrentUser handles PUT auth/me.
func (s *Server) UpdateCurrentUser(ctx context.Context, _ gen.UpdateCurrentUserRequestObject) (gen.UpdateCurrentUserResponseObject, error) {
	b := body(ctx)

	in, err := userInput(b)
	if err != nil {
		return nil, err
	}

	u, err := s.auth.UpdateMe(ctx, reqctx.Principal(ctx), in, stringPtr(b, "oldPassword"), stringPtr(b, "smsCode"))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.UpdateCurrentUser200JSONResponse(view.NewUser(u, s.urls, nil)), nil
}

// DeleteCurrentUser handles DELETE auth/me.
func (s *Server) DeleteCurrentUser(ctx context.Context, _ gen.DeleteCurrentUserRequestObject) (gen.DeleteCurrentUserResponseObject, error) {
	b := body(ctx)

	if err := s.auth.DeleteMe(ctx, reqctx.Principal(ctx), stringPtr(b, "oldPassword"), stringPtr(b, "smsCode")); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.DeleteCurrentUser200Response{Body: nil}, nil
}

// RefreshToken handles POST auth/refresh.
func (s *Server) RefreshToken(ctx context.Context, _ gen.RefreshTokenRequestObject) (gen.RefreshTokenResponseObject, error) {
	t, err := s.auth.Refresh(ctx, reqctx.Refresh(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.RefreshToken200JSONResponse(view.Refresh{Token: t.Token, RefreshToken: t.RefreshToken, TokenExpires: t.TokenExpires}), nil
}

// Logout handles POST auth/logout.
func (s *Server) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	if err := s.auth.Logout(ctx, reqctx.Principal(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.Logout200Response{Body: nil}, nil
}

// ChangePassword handles PUT auth/password.
func (s *Server) ChangePassword(ctx context.Context, req gen.ChangePasswordRequestObject) (gen.ChangePasswordResponseObject, error) {
	if err := s.auth.ChangePassword(ctx, reqctx.Principal(ctx), req.Body.OldPassword, req.Body.NewPassword); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.ChangePassword200Response{Body: nil}, nil
}

// GetMySessions handles GET auth/sessions.
func (s *Server) GetMySessions(ctx context.Context, _ gen.GetMySessionsRequestObject) (gen.GetMySessionsResponseObject, error) {
	p := reqctx.Principal(ctx)

	sessions, err := s.auth.Sessions(ctx, p)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	out := make(gen.GetMySessions200JSONResponse, len(sessions))
	for i := range sessions {
		out[i] = view.NewSession(&sessions[i], p.SessionID)
	}

	return out, nil
}

// RevokeMySession handles DELETE auth/sessions/:id.
func (s *Server) RevokeMySession(ctx context.Context, req gen.RevokeMySessionRequestObject) (gen.RevokeMySessionResponseObject, error) {
	if err := s.auth.RevokeSession(ctx, reqctx.Principal(ctx), int(req.Id)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.RevokeMySession200Response{Body: nil}, nil
}

// RevokeOtherSessions handles POST auth/sessions/revoke-others.
func (s *Server) RevokeOtherSessions(ctx context.Context, _ gen.RevokeOtherSessionsRequestObject) (gen.RevokeOtherSessionsResponseObject, error) {
	if err := s.auth.RevokeOtherSessions(ctx, reqctx.Principal(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.RevokeOtherSessions200Response{Body: nil}, nil
}
