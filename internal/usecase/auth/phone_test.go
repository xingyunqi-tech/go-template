package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/sms"
)

const _phone = "13800000000"

func expectFindByPhone(d *deps, u *entity.User) {
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Phone: ptr(_phone)}).Return(u, nil)
}

func phoneUser() *entity.User {
	u := activeUser()
	u.Email = nil
	u.Phone = ptr(_phone)
	u.Provider = entity.ProviderPhone

	return u
}

func TestSendCodeChecksPhoneAgainstPurpose(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		purpose string
		user    *entity.User
		field   string
		key     string
	}{
		{name: "register with taken phone", purpose: sms.PurposeRegister, user: phoneUser(), field: "phone", key: "phoneAlreadyExists"},
		{name: "login with unknown phone", purpose: sms.PurposeLogin, user: nil, field: "phone", key: "phoneNotFound"},
		{name: "reset with unknown phone", purpose: sms.PurposeResetPassword, user: nil, field: "phone", key: "phoneNotFound"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expectFindByPhone(d, tc.user)

			requireInvalid(t, svc.SendCode(context.Background(), _phone, tc.purpose), tc.field, tc.key)
		})
	}
}

func TestSendCodeRejectsChangePhonePurpose(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	requireInvalid(t, svc.SendCode(context.Background(), _phone, sms.PurposeChangePhone), "type", "unsupportedCodeType")
}

func TestSendCodeReportsSenderOutcome(t *testing.T) {
	t.Parallel()

	t.Run("sent", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, phoneUser())
		d.sender.EXPECT().SendCode(gomock.Any(), _phone, sms.PurposeLogin).Return(sms.SendResult{Sent: true}, nil)

		require.NoError(t, svc.SendCode(context.Background(), _phone, sms.PurposeLogin))
	})

	t.Run("resend interval", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, nil)
		d.sender.EXPECT().SendCode(gomock.Any(), _phone, sms.PurposeRegister).Return(sms.SendResult{RetryAfter: 42}, nil)

		err := svc.SendCode(context.Background(), _phone, sms.PurposeRegister)
		requireError(t, err, entity.StatusUnprocessable, "",
			entity.Detail{Name: "phone", Value: "rateLimited"}, entity.Detail{Name: "retryAfter", Value: float64(42)})
	})

	t.Run("provider failure", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, phoneUser())
		d.sender.EXPECT().SendCode(gomock.Any(), _phone, sms.PurposeLogin).Return(sms.SendResult{}, nil)

		requireInvalid(t, svc.SendCode(context.Background(), _phone, sms.PurposeLogin), "phone", "sendFailed")
	})

	t.Run("sender error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, phoneUser())
		d.sender.EXPECT().SendCode(gomock.Any(), _phone, sms.PurposeLogin).Return(sms.SendResult{}, errStore)

		require.ErrorIs(t, svc.SendCode(context.Background(), _phone, sms.PurposeLogin), errStore)
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		require.ErrorIs(t, svc.SendCode(context.Background(), _phone, sms.PurposeLogin), errStore)
	})
}

func TestSendCodeForEmptyPhoneSkipsLookup(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.sender.EXPECT().SendCode(gomock.Any(), "", sms.PurposeRegister).Return(sms.SendResult{Sent: true}, nil)

	require.NoError(t, svc.SendCode(context.Background(), "", sms.PurposeRegister))
}

func TestLoginWithPhone(t *testing.T) {
	t.Parallel()

	t.Run("signs in", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, phoneUser())
		expectVerify(d, true, _password)
		expectSession(d)

		login, err := svc.LoginWithPhone(context.Background(), _phone, _password, client())
		require.NoError(t, err)
		assert.Equal(t, _userID, login.User.ID)
	})

	t.Run("unknown phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, nil)

		_, err := svc.LoginWithPhone(context.Background(), _phone, _password, client())
		requireInvalid(t, err, "credentials", "incorrectPhoneOrPassword")
	})

	t.Run("wrong password", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, phoneUser())
		expectVerify(d, false, _password)

		_, err := svc.LoginWithPhone(context.Background(), _phone, _password, client())
		requireInvalid(t, err, "credentials", "incorrectPhoneOrPassword")
	})

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := phoneUser()
		u.Status = nil
		expectFindByPhone(d, u)
		expectVerify(d, true, _password)

		_, err := svc.LoginWithPhone(context.Background(), _phone, _password, client())
		requireInvalid(t, err, "phone", "userNotActive")
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.LoginWithPhone(context.Background(), _phone, _password, client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("verify error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectFindByPhone(d, phoneUser())
		d.hasher.EXPECT().VerifyAndUpdate(gomock.Any(), _stored, _password).Return(false, "", errStore)

		_, err := svc.LoginWithPhone(context.Background(), _phone, _password, client())
		require.ErrorIs(t, err, errStore)
	})
}

func expectCode(d *deps, purpose string, ok bool, err error) {
	d.sms.EXPECT().Verify(gomock.Any(), _phone, "1234", purpose).Return(ok, err)
}

func TestLoginWithSMS(t *testing.T) {
	t.Parallel()

	t.Run("signs in", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeLogin, true, nil)
		expectFindByPhone(d, phoneUser())
		expectSession(d)

		login, err := svc.LoginWithSMS(context.Background(), _phone, "1234", client())
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"id": float64(entity.RoleUser), "name": "User", "__entity": "RoleEntity"}, roleOf(t, login.Tokens.Token))
	})

	t.Run("invalid code", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeLogin, false, nil)

		_, err := svc.LoginWithSMS(context.Background(), _phone, "1234", client())
		requireInvalid(t, err, "code", "invalidCode")
	})

	t.Run("code error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeLogin, false, errStore)

		_, err := svc.LoginWithSMS(context.Background(), _phone, "1234", client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("unknown phone is not registered", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeLogin, true, nil)
		expectFindByPhone(d, nil)

		_, err := svc.LoginWithSMS(context.Background(), _phone, "1234", client())
		requireInvalid(t, err, "phone", "notFound")
	})

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeLogin, true, nil)
		expectFindByPhone(d, userWithStatus(3))

		_, err := svc.LoginWithSMS(context.Background(), _phone, "1234", client())
		requireInvalid(t, err, "phone", "userNotActive")
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeLogin, true, nil)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.LoginWithSMS(context.Background(), _phone, "1234", client())
		require.ErrorIs(t, err, errStore)
	})
}

func TestRegisterWithPhoneCreatesActiveUserAndSignsIn(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectCode(d, sms.PurposeRegister, true, nil)
	expectFindByPhone(d, nil)
	d.hasher.EXPECT().Hash(gomock.Any(), _password).Return("hashed", nil)
	expectFindByPhone(d, nil)
	expectCreate(d, func(row *repo.NewUser) {
		assert.Equal(t, entity.ProviderPhone, row.Provider)
		assert.Equal(t, ptr(_phone), row.Phone)
		assert.Equal(t, ptr("hashed"), row.Password)
		assert.Equal(t, ptr("Nick"), row.Nickname)
		assert.Equal(t, ptr(entity.RoleUser), row.RoleID)
		assert.Equal(t, ptr(entity.StatusActive), row.StatusID)
	}, phoneUser())
	d.settings.EXPECT().InitializeDefaultSettings(gomock.Any(), _userID).Return(nil)
	expectSession(d)

	login, err := svc.RegisterWithPhone(context.Background(), _phone, "1234", _password, ptr("Nick"), client())
	require.NoError(t, err)
	require.NotNil(t, login.Created)
	assert.Equal(t, ptr(entity.RoleUser), login.Created.RoleID)
	assert.Equal(t, map[string]any{"id": float64(entity.RoleUser)}, roleOf(t, login.Tokens.Token))
}

func TestRegisterWithPhoneRejects(t *testing.T) {
	t.Parallel()

	t.Run("invalid code", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeRegister, false, nil)

		_, err := svc.RegisterWithPhone(context.Background(), _phone, "1234", _password, nil, client())
		requireInvalid(t, err, "code", "invalidCode")
	})

	t.Run("taken phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeRegister, true, nil)
		expectFindByPhone(d, phoneUser())

		_, err := svc.RegisterWithPhone(context.Background(), _phone, "1234", _password, nil, client())
		requireInvalid(t, err, "phone", "phoneAlreadyExists")
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeRegister, true, nil)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.RegisterWithPhone(context.Background(), _phone, "1234", _password, nil, client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("create error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeRegister, true, nil)
		expectFindByPhone(d, nil)
		d.hasher.EXPECT().Hash(gomock.Any(), _password).Return("", errStore)

		_, err := svc.RegisterWithPhone(context.Background(), _phone, "1234", _password, nil, client())
		require.ErrorIs(t, err, errStore)
	})
}

func TestResetPasswordByPhone(t *testing.T) {
	t.Parallel()

	t.Run("replaces password and signs out everywhere", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeResetPassword, true, nil)
		expectFindByPhone(d, phoneUser())
		d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("fresh", nil)
		expectUpdate(d, func(c *repo.UserChanges) { assert.Equal(t, repo.Some("fresh"), c.Password) })
		expectFindByID(d, phoneUser())
		expectDeleteAll(d, nil, 1, 2)

		require.NoError(t, svc.ResetPasswordByPhone(context.Background(), _phone, "1234", "new"))
	})

	t.Run("invalid code", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeResetPassword, false, nil)

		requireInvalid(t, svc.ResetPasswordByPhone(context.Background(), _phone, "1234", "new"), "code", "invalidCode")
	})

	t.Run("code error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeResetPassword, false, errStore)

		require.ErrorIs(t, svc.ResetPasswordByPhone(context.Background(), _phone, "1234", "new"), errStore)
	})

	t.Run("unknown phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeResetPassword, true, nil)
		expectFindByPhone(d, nil)

		requireInvalid(t, svc.ResetPasswordByPhone(context.Background(), _phone, "1234", "new"), "phone", "phoneNotExists")
	})

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeResetPassword, true, nil)
		expectFindByPhone(d, userWithStatus(3))

		requireInvalid(t, svc.ResetPasswordByPhone(context.Background(), _phone, "1234", "new"), "phone", "userNotActive")
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeResetPassword, true, nil)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		require.ErrorIs(t, svc.ResetPasswordByPhone(context.Background(), _phone, "1234", "new"), errStore)
	})

	t.Run("update error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectCode(d, sms.PurposeResetPassword, true, nil)
		expectFindByPhone(d, phoneUser())
		d.hasher.EXPECT().Hash(gomock.Any(), "new").Return("", errStore)

		require.ErrorIs(t, svc.ResetPasswordByPhone(context.Background(), _phone, "1234", "new"), errStore)
	})
}
