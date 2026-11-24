package v1

import (
	"context"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

const _bearer = "Bearer"

func userOf(u *entity.User, avatarURL *string) gen.User {
	roles := u.Roles
	if roles == nil {
		roles = []string{}
	}

	return gen.User{
		Id: u.ID, Email: u.Email, EmailVerified: u.EmailVerified, Phone: u.Phone, DisplayName: u.DisplayName,
		AvatarUrl: avatarURL, Status: gen.UserStatus(u.Status), Roles: roles, HasPassword: u.HasPassword,
		CreatedAt: jsontime.New(u.CreatedAt), UpdatedAt: jsontime.New(u.UpdatedAt),
	}
}

// userViews renders users with the signed URLs of their avatars; a deleted avatar file has no URL.
func (s *Server) userViews(ctx context.Context, users []entity.User) ([]gen.User, error) {
	var ids []uuid.UUID

	for i := range users {
		if users[i].AvatarFileID != nil {
			ids = append(ids, *users[i].AvatarFileID)
		}
	}

	urls := map[uuid.UUID]string{}

	if len(ids) > 0 && s.files != nil {
		var err error

		if urls, err = s.files.URLs(ctx, ids); err != nil {
			return nil, err //nolint:wrapcheck // use case errors
		}
	}

	out := make([]gen.User, 0, len(users))

	for i := range users {
		var url *string

		if id := users[i].AvatarFileID; id != nil {
			if u, ok := urls[*id]; ok {
				url = &u
			}
		}

		out = append(out, userOf(&users[i], url))
	}

	return out, nil
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
