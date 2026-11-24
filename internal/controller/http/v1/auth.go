package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
)

func (s *Server) loginEnvelope(ctx context.Context, r *usecase.LoginResult) (gen.LoginResponseEnvelope, error) {
	views, err := s.userViews(ctx, []entity.User{*r.User})
	if err != nil {
		return gen.LoginResponseEnvelope{}, err
	}

	return gen.LoginResponseEnvelope{Code: response.Code, Message: response.Message, Data: gen.LoginResponse{
		AccessToken: r.AccessToken, RefreshToken: r.RefreshToken, ExpiresIn: int(r.ExpiresIn), TokenType: _bearer, User: views[0],
	}}, nil
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

	env, err := s.loginEnvelope(ctx, r)
	if err != nil {
		return nil, err
	}

	return gen.RegisterWithEmail201JSONResponse(env), nil
}

// LoginWithEmail handles POST /api/v1/auth/email/login.
func (s *Server) LoginWithEmail(ctx context.Context, req gen.LoginWithEmailRequestObject) (gen.LoginWithEmailResponseObject, error) {
	r, err := s.auth.Login(ctx, client(ctx), string(req.Body.Email), req.Body.Password)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	env, err := s.loginEnvelope(ctx, r)
	if err != nil {
		return nil, err
	}

	return gen.LoginWithEmail200JSONResponse(env), nil
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

	env, err := s.loginEnvelope(ctx, r)
	if err != nil {
		return nil, err
	}

	return gen.RefreshToken200JSONResponse(env), nil
}

// Logout handles POST /api/v1/auth/logout.
func (s *Server) Logout(ctx context.Context, _ gen.LogoutRequestObject) (gen.LogoutResponseObject, error) {
	if err := s.auth.Logout(ctx, caller(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.Logout200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// SendSmsCode handles POST /api/v1/auth/sms/send.
// bind_phone needs the token, so a rejected one answers with its own error.
func (s *Server) SendSmsCode(ctx context.Context, req gen.SendSmsCodeRequestObject) (gen.SendSmsCodeResponseObject, error) {
	c := optionalCaller(ctx)
	if c == nil && req.Body.Scene == gen.BindPhone {
		return nil, reqctx.AuthError(ctx) //nolint:wrapcheck // entity errors
	}

	if err := s.sms.Send(ctx, c, req.Body.Phone, string(req.Body.Scene)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.SendSmsCode202JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}

// LoginWithSms handles POST /api/v1/auth/sms/login.
func (s *Server) LoginWithSms(ctx context.Context, req gen.LoginWithSmsRequestObject) (gen.LoginWithSmsResponseObject, error) {
	r, err := s.auth.LoginWithSMS(ctx, client(ctx), req.Body.Phone, req.Body.Code)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	env, err := s.loginEnvelope(ctx, r)
	if err != nil {
		return nil, err
	}

	return gen.LoginWithSms200JSONResponse(env), nil
}

// ListAuthProviders handles GET /api/v1/auth/providers.
func (s *Server) ListAuthProviders(_ context.Context, _ gen.ListAuthProvidersRequestObject) (gen.ListAuthProvidersResponseObject, error) {
	ids := s.auth.Providers()

	items := make([]gen.AuthProvider, 0, len(ids))
	for _, id := range ids {
		items = append(items, gen.AuthProvider{Id: id})
	}

	return gen.ListAuthProviders200JSONResponse{Code: response.Code, Message: response.Message, Data: items}, nil
}

// LoginWithProvider handles POST /api/v1/auth/providers/{provider}/login.
func (s *Server) LoginWithProvider(ctx context.Context, req gen.LoginWithProviderRequestObject) (gen.LoginWithProviderResponseObject, error) {
	r, err := s.auth.LoginWithProvider(ctx, client(ctx), req.Provider, req.Body.Code)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	env, err := s.loginEnvelope(ctx, r)
	if err != nil {
		return nil, err
	}

	return gen.LoginWithProvider200JSONResponse(env), nil
}
