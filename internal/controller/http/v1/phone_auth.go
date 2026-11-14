package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
)

// SendSmsCode handles POST auth/phone/send-code.
func (s *Server) SendSmsCode(ctx context.Context, req gen.SendSmsCodeRequestObject) (gen.SendSmsCodeResponseObject, error) {
	purpose := sms.PurposeLogin
	if req.Body.Type != nil {
		purpose = string(*req.Body.Type)
	}

	if err := s.auth.SendCode(ctx, req.Body.Phone, purpose); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.SendSmsCode200Response{Body: nil}, nil
}

// LoginWithPhone handles POST auth/phone/login.
func (s *Server) LoginWithPhone(ctx context.Context, req gen.LoginWithPhoneRequestObject) (gen.LoginWithPhoneResponseObject, error) {
	l, err := s.auth.LoginWithPhone(ctx, req.Body.Phone, req.Body.Password, reqctx.Client(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.LoginWithPhone200JSONResponse(s.login(l)), nil
}

// LoginWithSms handles POST auth/phone/sms-login.
func (s *Server) LoginWithSms(ctx context.Context, req gen.LoginWithSmsRequestObject) (gen.LoginWithSmsResponseObject, error) {
	l, err := s.auth.LoginWithSMS(ctx, req.Body.Phone, req.Body.Code, reqctx.Client(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.LoginWithSms200JSONResponse(s.login(l)), nil
}

// RegisterWithPhone handles POST auth/phone/register.
func (s *Server) RegisterWithPhone(ctx context.Context, req gen.RegisterWithPhoneRequestObject) (gen.RegisterWithPhoneResponseObject, error) {
	b := req.Body

	l, err := s.auth.RegisterWithPhone(ctx, b.Phone, b.Code, b.Password, b.Nickname, reqctx.Client(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.RegisterWithPhone200JSONResponse(s.login(l)), nil
}

// ResetPasswordByPhone handles POST auth/phone/reset-password.
func (s *Server) ResetPasswordByPhone(ctx context.Context, req gen.ResetPasswordByPhoneRequestObject) (gen.ResetPasswordByPhoneResponseObject, error) {
	if err := s.auth.ResetPasswordByPhone(ctx, req.Body.Phone, req.Body.Code, req.Body.Password); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.ResetPasswordByPhone200Response{Body: nil}, nil
}

// LoginWithWechat handles POST auth/wechat/login.
func (s *Server) LoginWithWechat(ctx context.Context, req gen.LoginWithWechatRequestObject) (gen.LoginWithWechatResponseObject, error) {
	l, err := s.auth.LoginWithWechat(ctx, req.Body.Code, string(req.Body.Type), req.Body.Nickname, reqctx.Client(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.LoginWithWechat200JSONResponse(s.login(l)), nil
}

// LoginWithWechatPhone handles POST auth/wechat/phone-login.
func (s *Server) LoginWithWechatPhone(ctx context.Context, req gen.LoginWithWechatPhoneRequestObject) (gen.LoginWithWechatPhoneResponseObject, error) {
	l, err := s.auth.LoginWithWechatPhone(ctx, req.Body.LoginCode, req.Body.PhoneCode, reqctx.Client(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.LoginWithWechatPhone200JSONResponse(s.login(l)), nil
}

// LoginWithAlipay handles POST auth/alipay/login.
func (s *Server) LoginWithAlipay(ctx context.Context, req gen.LoginWithAlipayRequestObject) (gen.LoginWithAlipayResponseObject, error) {
	l, err := s.auth.LoginWithAlipay(ctx, req.Body.AuthCode, reqctx.Client(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.LoginWithAlipay200JSONResponse(s.login(l)), nil
}
