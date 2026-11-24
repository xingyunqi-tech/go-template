package user_test

import (
	"context"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
)

type noRevoker struct{}

func (noRevoker) Revoke(context.Context, uuid.UUID, time.Duration) error { return nil }

func newService(t *testing.T) (*user.Service, *mocks.MockUsers) {
	t.Helper()

	ctrl := gomock.NewController(t)
	users, sessions := mocks.NewMockUsers(ctrl), mocks.NewMockSessions(ctrl)

	return user.New(&user.Deps{
		Users: users, Revocation: session.NewRevocation(sessions, noRevoker{}, time.Minute), Hasher: nil,
		Limits: usecase.Limits{Limiter: nil, Enabled: false}, Now: nil, Outside: nil,
	}), users
}

func fieldErrors(t *testing.T, err error) []entity.FieldError {
	t.Helper()

	var verr *entity.ValidationError
	require.ErrorAs(t, err, &verr)

	return verr.Errors
}

func admin(id uuid.UUID) *repo.UserRecord {
	return &repo.UserRecord{User: entity.User{ID: id, Status: entity.UserActive, Roles: []string{"admin", "user"}}}
}

func TestSetRolesNeedsUserAndKnownRoles(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	id := uuid.NewV7()

	_, err := svc.SetRoles(t.Context(), id, []string{"admin"})
	assert.Equal(t, []entity.FieldError{{Field: "roles", Rule: entity.RuleContains, Params: map[string]any{"value": "user"}}}, fieldErrors(t, err))

	_, err = svc.SetRoles(t.Context(), id, []string{"user", "owner"})
	assert.Equal(t, "roles[1]", fieldErrors(t, err)[0].Field)
}

func TestTheLastActiveAdminKeepsTheRoleAndStaysActive(t *testing.T) {
	t.Parallel()

	svc, users := newService(t)
	id := uuid.NewV7()
	disabled := entity.UserDisabled

	users.EXPECT().GetForUpdate(gomock.Any(), id).Return(admin(id), true, nil).Times(2)
	users.EXPECT().LockAdmins(gomock.Any()).Return(nil).Times(2)
	users.EXPECT().CountActiveAdmins(gomock.Any(), id).Return(0, nil).Times(2)

	_, err := svc.Update(t.Context(), id, nil, &disabled)
	require.ErrorIs(t, err, entity.New(entity.KeyLastAdmin))

	_, err = svc.SetRoles(t.Context(), id, []string{"user"})
	require.ErrorIs(t, err, entity.New(entity.KeyLastAdmin))
}

func TestChangePasswordChecks(t *testing.T) {
	t.Parallel()

	svc, users := newService(t)
	caller := &usecase.Caller{UserID: uuid.NewV7(), SessionID: uuid.NewV7(), AuthTime: time.Now().Add(-time.Hour)}
	hash := "$argon2id$v=19$m=64,t=1,p=1$c2FsdA$a2V5"

	users.EXPECT().Get(gomock.Any(), caller.UserID).Return(&repo.UserRecord{User: entity.User{ID: caller.UserID}, PasswordHash: &hash}, true, nil)

	err := svc.ChangePassword(t.Context(), caller, &usecase.PasswordChange{Current: nil, New: "NewPassw0rd!"})
	assert.Equal(t, "currentPassword", fieldErrors(t, err)[0].Field)

	users.EXPECT().Get(gomock.Any(), caller.UserID).Return(&repo.UserRecord{User: entity.User{ID: caller.UserID}, PasswordHash: nil}, true, nil)

	err = svc.ChangePassword(t.Context(), caller, &usecase.PasswordChange{Current: nil, New: "NewPassw0rd!"})
	require.ErrorIs(t, err, entity.New(entity.KeyReauthRequired))
}

func TestInputsTheUseCaseRejects(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)

	_, err := svc.Create(t.Context(), &usecase.NewAccount{})
	assert.Len(t, fieldErrors(t, err), 2)

	bad := "12345"
	_, err = svc.Create(t.Context(), &usecase.NewAccount{Phone: &bad})
	assert.Equal(t, "phone", fieldErrors(t, err)[0].Field)

	_, err = svc.List(t.Context(), &usecase.UserQuery{Sort: "email,-email", Page: 1, PageSize: 10})
	assert.Equal(t, "query.sort", fieldErrors(t, err)[0].Field)
}

type avatars struct{ ok bool }

func (a avatars) IsAvatar(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return a.ok, nil }

func TestUpdateMeNeedsAnAvatarFileOfTheCaller(t *testing.T) {
	t.Parallel()

	users := mocks.NewMockUsers(gomock.NewController(t))
	caller := &usecase.Caller{UserID: uuid.NewV7()}
	avatar := uuid.NewV7()

	svc := user.New(&user.Deps{Users: users, Avatars: avatars{ok: false}})

	_, err := svc.UpdateMe(t.Context(), caller, &usecase.MeChanges{SetAvatar: true, AvatarFileID: &avatar})
	assert.Equal(t, entity.RuleExists, fieldErrors(t, err)[0].Rule)

	svc = user.New(&user.Deps{Users: users, Avatars: avatars{ok: true}})

	users.EXPECT().Update(gomock.Any(), caller.UserID, &repo.UserChanges{SetAvatar: true, AvatarFileID: &avatar}).Return(&entity.User{ID: caller.UserID}, true, nil)

	_, err = svc.UpdateMe(t.Context(), caller, &usecase.MeChanges{SetAvatar: true, AvatarFileID: &avatar})
	require.NoError(t, err)

	users.EXPECT().Update(gomock.Any(), caller.UserID, &repo.UserChanges{SetAvatar: true, AvatarFileID: nil}).Return(&entity.User{ID: caller.UserID}, true, nil)

	_, err = svc.UpdateMe(t.Context(), caller, &usecase.MeChanges{SetAvatar: true, AvatarFileID: nil})
	require.NoError(t, err)
}

type codes struct{ err error }

func (c codes) Verify(context.Context, string, string, string) error { return c.err }

type wechatPhone struct {
	phone string
	err   error
}

func (w wechatPhone) Phone(context.Context, string) (string, error) { return w.phone, w.err }

func phoneService(t *testing.T, verify error, wechat user.WechatPhone) (*user.Service, *mocks.MockUsers) {
	t.Helper()

	users := mocks.NewMockUsers(gomock.NewController(t))

	return user.New(&user.Deps{
		Users: users, Codes: codes{err: verify}, WechatPhone: wechat, Limits: usecase.Limits{Limiter: nil, Enabled: false},
	}), users
}

func TestBindPhoneSetsTheVerifiedPhone(t *testing.T) {
	t.Parallel()

	svc, users := phoneService(t, nil, nil)
	caller := &usecase.Caller{UserID: uuid.NewV7()}
	phone := "+8613800138000"

	users.EXPECT().SetPhone(gomock.Any(), caller.UserID, phone).Return(&entity.User{ID: caller.UserID, Phone: &phone}, true, nil)

	u, err := svc.BindPhone(t.Context(), caller, "13800138000", "123456")
	require.NoError(t, err)
	assert.Equal(t, phone, *u.Phone)
}

func TestBindPhoneRejectsAWrongCode(t *testing.T) {
	t.Parallel()

	svc, _ := phoneService(t, entity.New(entity.KeyVerificationCodeInvalid), nil)

	_, err := svc.BindPhone(t.Context(), &usecase.Caller{UserID: uuid.NewV7()}, "13800138000", "123456")
	require.ErrorIs(t, err, entity.New(entity.KeyVerificationCodeInvalid))
}

func TestBindWechatPhone(t *testing.T) {
	t.Parallel()

	svc, _ := phoneService(t, nil, nil)
	caller := &usecase.Caller{UserID: uuid.NewV7()}

	_, err := svc.BindWechatPhone(t.Context(), caller, "pc")
	require.ErrorIs(t, err, entity.New(entity.KeyProviderDisabled))

	svc, users := phoneService(t, nil, wechatPhone{phone: "+8613800138000"})
	users.EXPECT().SetPhone(gomock.Any(), caller.UserID, "+8613800138000").Return(nil, false, nil)

	_, err = svc.BindWechatPhone(t.Context(), caller, "pc")
	require.ErrorIs(t, err, entity.New(entity.KeyUserNotFound))

	svc, _ = phoneService(t, nil, wechatPhone{err: entity.New(entity.KeyBadGateway)})

	_, err = svc.BindWechatPhone(t.Context(), caller, "pc")
	require.ErrorIs(t, err, entity.New(entity.KeyBadGateway))
}
