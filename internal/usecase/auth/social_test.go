package auth_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
)

const (
	_openID     = "open-id"
	_alipayID   = "alipay-id"
	_loginCode  = "login-code"
	_phoneCode  = "phone-code"
	_otherUser  = 6
	_wechatName = "微信用户"
)

func expectFindByWechat(d *deps, u *entity.User) {
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{WechatOpenID: ptr(_openID)}).Return(u, nil)
}

func expectFindByAlipay(d *deps, u *entity.User) {
	d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{AlipayOpenID: ptr(_alipayID)}).Return(u, nil)
}

func wechatUser() *entity.User {
	u := activeUser()
	u.Email = nil
	u.Password = nil
	u.Provider = entity.ProviderWechat
	u.WechatOpenID = ptr(_openID)

	return u
}

func TestLoginWithWechatSignsInExistingUser(t *testing.T) {
	t.Parallel()

	cases := map[string]func(d *deps){
		auth.WechatMiniApp: func(d *deps) {
			d.wechat.EXPECT().MiniAppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)
		},
		auth.WechatApp: func(d *deps) {
			d.wechat.EXPECT().AppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)
		},
	}

	for kind, expectLogin := range cases {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expectLogin(d)
			expectFindByWechat(d, wechatUser())
			expectSession(d)

			login, err := svc.LoginWithWechat(context.Background(), _loginCode, kind, nil, client())
			require.NoError(t, err)
			assert.Nil(t, login.Created)
		})
	}
}

func TestLoginWithWechatCreatesUnknownUser(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		info     *entity.WechatUser
		nickname *string
		check    func(t *testing.T, row *repo.NewUser)
	}{
		{
			name: "defaults", info: &entity.WechatUser{OpenID: _openID},
			check: func(t *testing.T, row *repo.NewUser) {
				t.Helper()
				assert.Equal(t, ptr(_wechatName), row.Nickname)
				assert.Equal(t, ptr(0), row.Gender)
				assert.Nil(t, row.WechatUnionID)
			},
		},
		{
			name: "profile from wechat",
			info: &entity.WechatUser{OpenID: _openID, UnionID: ptr("union"), Nickname: ptr("WX"), Gender: ptr(1)},
			check: func(t *testing.T, row *repo.NewUser) {
				t.Helper()
				assert.Equal(t, ptr("WX"), row.Nickname)
				assert.Equal(t, ptr(1), row.Gender)
				assert.Equal(t, ptr("union"), row.WechatUnionID)
			},
		},
		{
			name: "requested nickname wins", info: &entity.WechatUser{OpenID: _openID, Nickname: ptr("WX")}, nickname: ptr("Mine"),
			check: func(t *testing.T, row *repo.NewUser) {
				t.Helper()
				assert.Equal(t, ptr("Mine"), row.Nickname)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			d.wechat.EXPECT().AppLogin(gomock.Any(), _loginCode).Return(tc.info, nil)
			expectFindByWechat(d, nil)
			expectCreate(d, func(row *repo.NewUser) {
				assert.Equal(t, entity.ProviderWechat, row.Provider)
				assert.Equal(t, ptr(_openID), row.WechatOpenID)
				assert.Equal(t, ptr(entity.StatusActive), row.StatusID)
				tc.check(t, row)
			}, wechatUser())
			d.settings.EXPECT().InitializeDefaultSettings(gomock.Any(), _userID).Return(nil)
			expectSession(d)

			login, err := svc.LoginWithWechat(context.Background(), _loginCode, auth.WechatApp, tc.nickname, client())
			require.NoError(t, err)
			require.NotNil(t, login.Created)
			assert.Equal(t, map[string]any{"id": float64(entity.RoleUser)}, roleOf(t, login.Tokens.Token))
		})
	}
}

func TestLoginWithWechatResolvesConcurrentCreation(t *testing.T) {
	t.Parallel()

	t.Run("user created concurrently", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.wechat.EXPECT().AppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)
		expectFindByWechat(d, nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)
		expectFindByWechat(d, wechatUser())
		expectSession(d)

		login, err := svc.LoginWithWechat(context.Background(), _loginCode, auth.WechatApp, nil, client())
		require.NoError(t, err)
		assert.Nil(t, login.Created)
	})

	t.Run("no user returns create error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.wechat.EXPECT().AppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)
		expectFindByWechat(d, nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)
		expectFindByWechat(d, nil)

		_, err := svc.LoginWithWechat(context.Background(), _loginCode, auth.WechatApp, nil, client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("lookup after create error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		lookupErr := assert.AnError

		d.wechat.EXPECT().AppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)
		expectFindByWechat(d, nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, lookupErr)

		_, err := svc.LoginWithWechat(context.Background(), _loginCode, auth.WechatApp, nil, client())
		require.ErrorIs(t, err, lookupErr)
	})
}

func TestLoginWithWechatRejects(t *testing.T) {
	t.Parallel()

	t.Run("wechat error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.wechat.EXPECT().MiniAppLogin(gomock.Any(), _loginCode).Return(nil, errStore)

		_, err := svc.LoginWithWechat(context.Background(), _loginCode, auth.WechatMiniApp, nil, client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.wechat.EXPECT().AppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.LoginWithWechat(context.Background(), _loginCode, auth.WechatApp, nil, client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := wechatUser()
		u.Status = nil

		d.wechat.EXPECT().AppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)
		expectFindByWechat(d, u)

		_, err := svc.LoginWithWechat(context.Background(), _loginCode, auth.WechatApp, nil, client())
		requireInvalid(t, err, "wechat", "userNotActive")
	})
}

func expectIdentity(d *deps, info *entity.WechatUser, countryCode string) {
	d.wechat.EXPECT().PhoneNumber(gomock.Any(), _phoneCode).Return(&entity.WechatPhone{Number: _phone, CountryCode: countryCode}, nil)
	d.wechat.EXPECT().MiniAppLogin(gomock.Any(), _loginCode).Return(info, nil)
}

func expectResolve(d *deps, byOpenID, byPhone *entity.User) {
	expectFindByWechat(d, byOpenID)
	expectFindByPhone(d, byPhone)
}

func loginWithWechatPhone(svc *auth.Service) (*auth.Login, error) {
	return svc.LoginWithWechatPhone(context.Background(), _loginCode, _phoneCode, client())
}

func TestLoginWithWechatPhoneResolvesIdentityErrors(t *testing.T) {
	t.Parallel()

	t.Run("login error cancels phone lookup", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.wechat.EXPECT().PhoneNumber(gomock.Any(), _phoneCode).DoAndReturn(func(ctx context.Context, _ string) (*entity.WechatPhone, error) {
			<-ctx.Done()

			return nil, ctx.Err()
		})
		d.wechat.EXPECT().MiniAppLogin(gomock.Any(), _loginCode).Return(nil, errStore)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("phone error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.wechat.EXPECT().PhoneNumber(gomock.Any(), _phoneCode).Return(nil, errStore)
		d.wechat.EXPECT().MiniAppLogin(gomock.Any(), _loginCode).Return(&entity.WechatUser{OpenID: _openID}, nil)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("foreign phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "1")

		_, err := loginWithWechatPhone(svc)
		requireError(t, err, entity.StatusUnprocessable, "phoneRegionNotSupported", entity.Detail{Name: "phone", Value: "unsupportedRegion"})
	})
}

func requireBoundToOtherWechat(t *testing.T, err error) {
	t.Helper()
	requireError(t, err, entity.StatusUnprocessable, "phoneBoundToOtherWechat", entity.Detail{Name: "phone", Value: "boundToOtherWechat"})
}

func TestLoginWithWechatPhoneSignsInMatchingUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	u := wechatUser()
	u.Phone = ptr(_phone)

	expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
	expectResolve(d, u, u)
	expectSession(d)

	login, err := loginWithWechatPhone(svc)
	require.NoError(t, err)
	assert.Nil(t, login.Created)
}

func TestLoginWithWechatPhoneRejectsPhoneOfOtherUser(t *testing.T) {
	t.Parallel()

	t.Run("open id and phone on different users", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		other := phoneUser()
		other.ID = _otherUser

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, wechatUser(), other)

		_, err := loginWithWechatPhone(svc)
		requireBoundToOtherWechat(t, err)
	})

	t.Run("phone user bound to another open id", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		other := phoneUser()
		other.WechatOpenID = ptr("another")

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, nil, other)

		_, err := loginWithWechatPhone(svc)
		requireBoundToOtherWechat(t, err)
	})
}

func TestLoginWithWechatPhoneCreatesUser(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
	expectResolve(d, nil, nil)
	expectFindByPhone(d, nil)
	expectCreate(d, func(row *repo.NewUser) {
		assert.Equal(t, ptr(_phone), row.Phone)
		assert.Equal(t, ptr(_openID), row.WechatOpenID)
		assert.Equal(t, ptr(_wechatName), row.Nickname)
	}, wechatUser())
	d.settings.EXPECT().InitializeDefaultSettings(gomock.Any(), _userID).Return(nil)
	expectSession(d)

	login, err := loginWithWechatPhone(svc)
	require.NoError(t, err)
	require.NotNil(t, login.Created)
}

func TestLoginWithWechatPhoneRetriesFailedCreation(t *testing.T) {
	t.Parallel()

	t.Run("second pass finds user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := wechatUser()
		u.Phone = ptr(_phone)

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, nil, nil)
		expectFindByPhone(d, &entity.User{ID: _otherUser})
		expectResolve(d, u, u)
		expectSession(d)

		_, err := loginWithWechatPhone(svc)
		require.NoError(t, err)
	})

	t.Run("second failure is returned", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, nil, nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)
		expectResolve(d, nil, nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Phone: ptr(_phone)}).Return(nil, nil).Times(2)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})
}

func TestLoginWithWechatPhoneBindsPhoneToWechatUser(t *testing.T) {
	t.Parallel()

	t.Run("binds", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		bound := wechatUser()
		bound.Phone = ptr(_phone)

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, wechatUser(), nil)
		d.bindings.EXPECT().BindPhoneIfEmpty(gomock.Any(), _userID, _phone).Return(true, nil)
		expectFindByID(d, bound)
		expectSession(d)

		login, err := loginWithWechatPhone(svc)
		require.NoError(t, err)
		assert.Same(t, bound, login.User)
	})

	t.Run("keeps existing phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := wechatUser()
		u.Phone = ptr("13900000000")

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, u, nil)
		expectSession(d)

		login, err := loginWithWechatPhone(svc)
		require.NoError(t, err)
		assert.Same(t, u, login.User)
	})

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := wechatUser()
		u.Status = nil

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, u, nil)

		_, err := loginWithWechatPhone(svc)
		requireInvalid(t, err, "wechat", "userNotActive")
	})

	t.Run("bind lost twice", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, wechatUser(), nil)
		expectResolve(d, wechatUser(), nil)
		d.bindings.EXPECT().BindPhoneIfEmpty(gomock.Any(), _userID, _phone).Return(false, nil).Times(2)

		_, err := loginWithWechatPhone(svc)
		require.EqualError(t, err, "auth: failed to bind phone")
	})

	t.Run("reloaded user missing twice", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, wechatUser(), nil)
		expectResolve(d, wechatUser(), nil)
		d.bindings.EXPECT().BindPhoneIfEmpty(gomock.Any(), _userID, _phone).Return(true, nil).Times(2)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(_userID)}).Return(nil, nil).Times(2)

		_, err := loginWithWechatPhone(svc)
		requireInvalid(t, err, "user", "notFound")
	})

	t.Run("reload error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, wechatUser(), nil)
		d.bindings.EXPECT().BindPhoneIfEmpty(gomock.Any(), _userID, _phone).Return(true, nil)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{ID: ptr(_userID)}).Return(nil, errStore)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})
}

func TestLoginWithWechatPhoneBindsWechatToPhoneUser(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		unionID *string
		want    *string
	}{
		{name: "with union id", unionID: ptr("union"), want: ptr("union")},
		{name: "empty union id", unionID: ptr(""), want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			expectIdentity(d, &entity.WechatUser{OpenID: _openID, UnionID: tc.unionID}, "86")
			expectResolve(d, nil, phoneUser())
			d.bindings.EXPECT().BindWechatIfEmpty(gomock.Any(), _userID, _openID, tc.want).Return(true, nil)
			expectFindByID(d, phoneUser())
			expectSession(d)

			_, err := loginWithWechatPhone(svc)
			require.NoError(t, err)
		})
	}
}

func TestLoginWithWechatPhoneBindWechatFailures(t *testing.T) {
	t.Parallel()

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := phoneUser()
		u.Status = nil

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, nil, u)

		_, err := loginWithWechatPhone(svc)
		requireInvalid(t, err, "wechat", "userNotActive")
	})

	t.Run("bind lost twice", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, nil, phoneUser())
		expectResolve(d, nil, phoneUser())
		d.bindings.EXPECT().BindWechatIfEmpty(gomock.Any(), _userID, _openID, nil).Return(false, nil).Times(2)

		_, err := loginWithWechatPhone(svc)
		require.EqualError(t, err, "auth: failed to bind wechat")
	})

	t.Run("bind error twice", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, nil, phoneUser())
		expectResolve(d, nil, phoneUser())
		d.bindings.EXPECT().BindWechatIfEmpty(gomock.Any(), _userID, _openID, nil).Return(false, errStore).Times(2)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})
}

func TestLoginWithWechatPhoneLookupErrors(t *testing.T) {
	t.Parallel()

	t.Run("open id", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{WechatOpenID: ptr(_openID)}).Return(nil, errStore)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("phone", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectFindByWechat(d, nil)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{Phone: ptr(_phone)}).Return(nil, errStore)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("second pass", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, wechatUser(), nil)
		d.bindings.EXPECT().BindPhoneIfEmpty(gomock.Any(), _userID, _phone).Return(false, nil)
		d.users.EXPECT().Get(gomock.Any(), repo.UserQuery{WechatOpenID: ptr(_openID)}).Return(nil, errStore)

		_, err := loginWithWechatPhone(svc)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("blocked resolved user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := wechatUser()
		u.Phone = ptr(_phone)
		u.Status = nil

		expectIdentity(d, &entity.WechatUser{OpenID: _openID}, "86")
		expectResolve(d, u, u)

		_, err := loginWithWechatPhone(svc)
		requireInvalid(t, err, "wechat", "userNotActive")
	})
}

func alipayUser() *entity.User {
	u := activeUser()
	u.Email = nil
	u.Password = nil
	u.Provider = entity.ProviderAlipay
	u.AlipayOpenID = ptr(_alipayID)

	return u
}

func TestLoginWithAlipay(t *testing.T) {
	t.Parallel()

	t.Run("signs in existing user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return(_alipayID, nil)
		expectFindByAlipay(d, alipayUser())
		expectSession(d)

		login, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		require.NoError(t, err)
		assert.Nil(t, login.Created)
	})

	t.Run("creates unknown user", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return(_alipayID, nil)
		expectFindByAlipay(d, nil)
		expectCreate(d, func(row *repo.NewUser) {
			assert.Equal(t, entity.ProviderAlipay, row.Provider)
			assert.Equal(t, ptr(_alipayID), row.AlipayOpenID)
			assert.Equal(t, ptr("支付宝用户"), row.Nickname)
			assert.Equal(t, ptr(entity.StatusActive), row.StatusID)
		}, alipayUser())
		d.settings.EXPECT().InitializeDefaultSettings(gomock.Any(), _userID).Return(nil)
		expectSession(d)

		login, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		require.NoError(t, err)
		require.NotNil(t, login.Created)
	})

	t.Run("user created concurrently", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return(_alipayID, nil)
		expectFindByAlipay(d, nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)
		expectFindByAlipay(d, alipayUser())
		expectSession(d)

		_, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		require.NoError(t, err)
	})

	t.Run("no user returns create error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return(_alipayID, nil)
		expectFindByAlipay(d, nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)
		expectFindByAlipay(d, nil)

		_, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		require.ErrorIs(t, err, errStore)
	})
}

func TestLoginWithAlipayRejects(t *testing.T) {
	t.Parallel()

	t.Run("exchange error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return("", errStore)

		_, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("empty open id with failed creation", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return("", nil)
		d.users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(0, errStore)

		_, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("lookup error", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return(_alipayID, nil)
		d.users.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		require.ErrorIs(t, err, errStore)
	})

	t.Run("blocked status", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		u := alipayUser()
		u.Status = &entity.Named{ID: 3}

		d.alipay.EXPECT().ExchangeAuthCode(gomock.Any(), "auth").Return(_alipayID, nil)
		expectFindByAlipay(d, u)

		_, err := svc.LoginWithAlipay(context.Background(), "auth", client())
		requireInvalid(t, err, "alipay", "userNotActive")
	})
}
