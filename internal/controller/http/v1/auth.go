package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
)

func loginEnvelope(r *usecase.LoginResult) gen.LoginResponseEnvelope {
	return gen.LoginResponseEnvelope{Code: response.Code, Message: response.Message, Data: loginOf(r)}
}

// RegisterWithEmail handles POST /api/v1/auth/email/register.
func (s *Server) RegisterWithEmail(ctx context.Context, req gen.RegisterWithEmailRequestObject) (gen.RegisterWithEmailResponseObject, error) {
	name := ""
	if req.Body.DisplayName != nil {
		name = *req.Body.DisplayName
	}

	r, err := s.auth.Register(ctx, client(ctx), &usecase.Registration{Email: string(req.Body.Email), Password: req.Body.Password, DisplayName: name})
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.RegisterWithEmail201JSONResponse(loginEnvelope(r)), nil
}

// LoginWithEmail handles POST /api/v1/auth/email/login.
func (s *Server) LoginWithEmail(ctx context.Context, req gen.LoginWithEmailRequestObject) (gen.LoginWithEmailResponseObject, error) {
	r, err := s.auth.Login(ctx, client(ctx), string(req.Body.Email), req.Body.Password)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.LoginWithEmail200JSONResponse(loginEnvelope(r)), nil
}

// VerifyEmail handles POST /api/v1/auth/email/verify.
func (s *Server) VerifyEmail(ctx context.Context, req gen.VerifyEmailRequestObject) (gen.VerifyEmailResponseObject, error) {
	if err := s.auth.VerifyEmail(ctx, req.Body.Token); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.VerifyEmail200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// ResendVerificationEmail handles POST /api/v1/auth/email/verify/resend.
func (s *Server) ResendVerificationEmail(ctx context.Context, _ gen.ResendVerificationEmailRequestObject) (gen.ResendVerificationEmailResponseObject, error) {
	if err := s.auth.ResendVerification(ctx, caller(ctx), i18n.Language(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.ResendVerificationEmail202JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// ForgotPassword handles POST /api/v1/auth/password/forgot.
func (s *Server) ForgotPassword(ctx context.Context, req gen.ForgotPasswordRequestObject) (gen.ForgotPasswordResponseObject, error) {
	if err := s.auth.ForgotPassword(ctx, client(ctx), string(req.Body.Email)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.ForgotPassword202JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// ResetPassword handles POST /api/v1/auth/password/reset.
func (s *Server) ResetPassword(ctx context.Context, req gen.ResetPasswordRequestObject) (gen.ResetPasswordResponseObject, error) {
	userID, err := s.auth.ResetPassword(ctx, req.Body.Token, req.Body.Password)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	reqctx.SetAuditTarget(ctx, userID, userID)

	return gen.ResetPassword200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// RefreshToken handles POST /api/v1/auth/refresh.
func (s *Server) RefreshToken(ctx context.Context, req gen.RefreshTokenRequestObject) (gen.RefreshTokenResponseObject, error) {
	r, err := s.auth.Refresh(ctx, client(ctx), req.Body.RefreshToken)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.RefreshToken200JSONResponse(loginEnvelope(r)), nil
}

// Logout handles POST /api/v1/auth/logout.
func (s *Server) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	if err := s.auth.Logout(ctx, caller(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.Logout200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}
