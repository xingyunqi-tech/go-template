package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

const _bearer = "Bearer"

func userOf(u *entity.User) gen.User {
	roles := u.Roles
	if roles == nil {
		roles = []string{}
	}

	return gen.User{
		Id: u.ID, Email: u.Email, EmailVerified: u.EmailVerified, Phone: u.Phone, DisplayName: u.DisplayName,
		AvatarUrl: nil, Status: gen.UserStatus(u.Status), Roles: roles, HasPassword: u.HasPassword,
		CreatedAt: jsontime.New(u.CreatedAt), UpdatedAt: jsontime.New(u.UpdatedAt),
	}
}

func loginOf(r *usecase.LoginResult) gen.LoginResponse {
	return gen.LoginResponse{
		AccessToken: r.AccessToken, RefreshToken: r.RefreshToken, ExpiresIn: int(r.ExpiresIn), TokenType: _bearer,
		User: userOf(r.User),
	}
}

func sessionOf(v *usecase.SessionView) gen.Session {
	s := v.Session

	return gen.Session{
		Id: s.ID, UserAgent: s.UserAgent, Ip: s.IP, CreatedAt: jsontime.New(s.CreatedAt),
		LastActiveAt: jsontime.New(s.LastActiveAt), Current: v.Current,
	}
}

// client is the device of the request.
func client(ctx context.Context) *usecase.Client {
	c := reqctx.ClientOf(ctx)

	return &usecase.Client{IP: c.IP, UserAgent: c.UserAgent, Locale: i18n.Language(ctx)}
}

// caller is the signed-in user of the request.
func caller(ctx context.Context) *usecase.Caller {
	p := reqctx.PrincipalOf(ctx)

	return &usecase.Caller{UserID: p.Subject, SessionID: p.SessionID, AuthTime: p.AuthTime}
}

// optionalCaller is the signed-in user of the request, nil for an anonymous one.
func optionalCaller(ctx context.Context) *usecase.Caller {
	if reqctx.PrincipalOf(ctx) == nil {
		return nil
	}

	return caller(ctx)
}
