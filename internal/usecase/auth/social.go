package auth

import (
	"context"
	"errors"
	"sync"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
)

// WeChat sign-in types.
const (
	WechatMiniApp = "mini_app"
	WechatApp     = "app"
)

const (
	_chinaCountryCode = "86"
	_wechatNickname   = "微信用户"
	_alipayNickname   = "支付宝用户"
)

var (
	errBindPhone  = errors.New("auth: failed to bind phone")
	errBindWechat = errors.New("auth: failed to bind wechat")
)

// Wechat resolves WeChat sign-in codes.
type Wechat interface {
	MiniAppLogin(ctx context.Context, code string) (*entity.WechatUser, error)
	AppLogin(ctx context.Context, code string) (*entity.WechatUser, error)
	PhoneNumber(ctx context.Context, code string) (*entity.WechatPhone, error)
}

// Alipay resolves Alipay auth codes.
type Alipay interface {
	ExchangeAuthCode(ctx context.Context, authCode string) (string, error)
}

func (s *Service) findByWechat(ctx context.Context, openID string) (*entity.User, error) {
	if openID == "" {
		return nil, nil //nolint:nilnil // empty ids match nobody
	}

	return s.findBy(ctx, repo.UserQuery{WechatOpenID: &openID})
}

func (s *Service) findByAlipay(ctx context.Context, openID string) (*entity.User, error) {
	if openID == "" {
		return nil, nil //nolint:nilnil // empty ids match nobody
	}

	return s.findBy(ctx, repo.UserQuery{AlipayOpenID: &openID})
}

type created struct {
	user    *entity.User
	created *user.Created
}

func (s *Service) createWechatUser(ctx context.Context, info *entity.WechatUser, phone, nickname *string, gender *int) (*created, error) {
	in := active(entity.ProviderWechat)
	in.WechatOpenID = repo.Some(info.OpenID)
	in.Nickname = repo.Some(_wechatNickname)
	in.Gender = repo.Some(0)

	if info.UnionID != nil {
		in.WechatUnionID = repo.Some(*info.UnionID)
	}

	if phone != nil {
		in.Phone = repo.Some(*phone)
	}

	if nickname != nil {
		in.Nickname = repo.Some(*nickname)
	}

	if gender != nil {
		in.Gender = repo.Some(*gender)
	}

	u, c, err := s.users.Create(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // user errors are wrapped
	}

	s.initializeSettings(ctx, u.ID)

	return &created{user: u, created: c}, nil
}

// LoginWithWechat is POST auth/wechat/login: unknown open ids get a new user.
func (s *Service) LoginWithWechat(ctx context.Context, code, kind string, nickname *string, client Client) (*Login, error) {
	login := s.wechat.AppLogin
	if kind == WechatMiniApp {
		login = s.wechat.MiniAppLogin
	}

	info, err := login(ctx, code)
	if err != nil {
		return nil, err //nolint:wrapcheck // wechat errors are HTTP errors
	}

	u, err := s.findByWechat(ctx, info.OpenID)
	if err != nil {
		return nil, err
	}

	if u == nil {
		if nickname == nil {
			nickname = info.Nickname
		}

		c, createErr := s.createWechatUser(ctx, info, nil, nickname, info.Gender)
		if createErr == nil {
			return s.signInCreated(ctx, c.user, c.created, client)
		}

		if u, err = s.concurrentlyCreated(ctx, createErr, func() (*entity.User, error) { return s.findByWechat(ctx, info.OpenID) }); err != nil {
			return nil, err
		}
	}

	if err := s.ensureCanAuthenticate(ctx, u, "wechat"); err != nil {
		return nil, err
	}

	return s.signIn(ctx, u, client)
}

// concurrentlyCreated finds the user a concurrent request created, or returns createErr.
func (s *Service) concurrentlyCreated(ctx context.Context, createErr error, find func() (*entity.User, error)) (*entity.User, error) {
	u, err := find()
	if err != nil {
		return nil, err
	}

	if u == nil {
		return nil, createErr
	}

	s.log.InfoContext(ctx, "Concurrent registration resolved to user", "user_id", u.ID)

	return u, nil
}

// LoginWithWechatPhone is POST auth/wechat/phone-login: binds the open id and the mainland phone
// number to one user without merging accounts.
func (s *Service) LoginWithWechatPhone(ctx context.Context, loginCode, phoneCode string, client Client) (*Login, error) {
	info, phone, err := s.wechatIdentity(ctx, loginCode, phoneCode)
	if err != nil {
		return nil, err
	}

	if phone.CountryCode != _chinaCountryCode {
		s.log.WarnContext(ctx, "WeChat phone login rejected - unsupported country code", "country_code", phone.CountryCode)

		return nil, entity.UnprocessableKey("phoneRegionNotSupported", entity.Detail{Name: "phone", Value: "unsupportedRegion"})
	}

	found, err := s.resolveWechatPhoneUser(ctx, info, phone.Number, true)
	if err != nil {
		return nil, err
	}

	if found == nil {
		if found, err = s.resolveWechatPhoneUser(ctx, info, phone.Number, false); err != nil {
			return nil, err
		}
	}

	if found == nil {
		return nil, entity.Invalid("user", "notFound")
	}

	if err := s.ensureCanAuthenticate(ctx, found.user, "wechat"); err != nil {
		return nil, err
	}

	if found.created != nil {
		return s.signInCreated(ctx, found.user, found.created, client)
	}

	return s.signIn(ctx, found.user, client)
}

// wechatIdentity resolves both codes concurrently; a login error cancels the phone lookup and wins.
func (s *Service) wechatIdentity(ctx context.Context, loginCode, phoneCode string) (*entity.WechatUser, *entity.WechatPhone, error) {
	var (
		wg       sync.WaitGroup
		info     *entity.WechatUser
		phone    *entity.WechatPhone
		phoneErr error
	)

	phoneCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	wg.Go(func() { phone, phoneErr = s.wechat.PhoneNumber(phoneCtx, phoneCode) })

	info, err := s.wechat.MiniAppLogin(ctx, loginCode)
	if err != nil {
		cancel()
	}

	wg.Wait()

	if err != nil {
		return nil, nil, err //nolint:wrapcheck // wechat errors are HTTP errors
	}

	if phoneErr != nil {
		return nil, nil, phoneErr //nolint:wrapcheck // wechat errors are HTTP errors
	}

	return info, phone, nil
}

func phoneBoundToOtherWechat() *entity.Error {
	return entity.UnprocessableKey("phoneBoundToOtherWechat", entity.Detail{Name: "phone", Value: "boundToOtherWechat"})
}

// resolveWechatPhoneUser finds, binds or creates the user of an open id and phone; with
// allowRetry a failed bind or create returns nil so that the caller resolves again.
func (s *Service) resolveWechatPhoneUser(ctx context.Context, info *entity.WechatUser, phone string, allowRetry bool) (*created, error) {
	byOpenID, err := s.findByWechat(ctx, info.OpenID)
	if err != nil {
		return nil, err
	}

	byPhone, err := s.findByPhone(ctx, phone)
	if err != nil {
		return nil, err
	}

	retryOr := func(err error) (*created, error) {
		if allowRetry {
			return nil, nil //nolint:nilnil // nil asks the caller to resolve again
		}

		return nil, err
	}

	switch {
	case byOpenID != nil && byPhone != nil:
		if byOpenID.ID != byPhone.ID {
			s.log.WarnContext(ctx, "WeChat phone login rejected - phone bound to another user", "phone", entity.MaskPhone(phone))

			return nil, phoneBoundToOtherWechat()
		}

		return &created{user: byOpenID, created: nil}, nil
	case byOpenID != nil:
		return s.bindPhone(ctx, byOpenID, phone, retryOr)
	case byPhone != nil:
		return s.bindWechat(ctx, byPhone, info, phone, retryOr)
	}

	c, err := s.createWechatUser(ctx, info, &phone, nil, nil)
	if err != nil {
		return retryOr(err)
	}

	return c, nil
}

func (s *Service) bindPhone(ctx context.Context, u *entity.User, phone string, retryOr func(error) (*created, error)) (*created, error) {
	if u.Phone != nil && *u.Phone != "" {
		return &created{user: u, created: nil}, nil
	}

	if err := s.ensureCanAuthenticate(ctx, u, "wechat"); err != nil {
		return nil, err
	}

	bound, err := s.bindings.BindPhoneIfEmpty(ctx, u.ID, phone)
	if err == nil && !bound {
		err = errBindPhone
	}

	if err != nil {
		return retryOr(err)
	}

	return s.reload(ctx, u.ID)
}

func (s *Service) bindWechat(ctx context.Context, u *entity.User, info *entity.WechatUser, phone string, retryOr func(error) (*created, error)) (*created, error) {
	if u.WechatOpenID != nil && *u.WechatOpenID != "" {
		s.log.WarnContext(ctx, "WeChat phone login rejected - phone bound to another user", "phone", entity.MaskPhone(phone))

		return nil, phoneBoundToOtherWechat()
	}

	if err := s.ensureCanAuthenticate(ctx, u, "wechat"); err != nil {
		return nil, err
	}

	var unionID *string
	if info.UnionID != nil && *info.UnionID != "" {
		unionID = info.UnionID
	}

	bound, err := s.bindings.BindWechatIfEmpty(ctx, u.ID, info.OpenID, unionID)
	if err == nil && !bound {
		err = errBindWechat
	}

	if err != nil {
		return retryOr(err)
	}

	return s.reload(ctx, u.ID)
}

func (s *Service) reload(ctx context.Context, id int) (*created, error) {
	u, err := s.users.FindByID(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // user errors are wrapped
	}

	if u == nil {
		return nil, nil //nolint:nilnil // nil asks the caller to resolve again
	}

	return &created{user: u, created: nil}, nil
}

// LoginWithAlipay is POST auth/alipay/login: unknown open ids get a new user.
func (s *Service) LoginWithAlipay(ctx context.Context, authCode string, client Client) (*Login, error) {
	openID, err := s.alipay.ExchangeAuthCode(ctx, authCode)
	if err != nil {
		return nil, err //nolint:wrapcheck // alipay errors are HTTP errors
	}

	u, err := s.findByAlipay(ctx, openID)
	if err != nil {
		return nil, err
	}

	if u == nil {
		in := active(entity.ProviderAlipay)
		in.AlipayOpenID = repo.Some(openID)
		in.Nickname = repo.Some(_alipayNickname)

		nu, c, createErr := s.users.Create(ctx, in)
		if createErr == nil {
			s.initializeSettings(ctx, nu.ID)

			return s.signInCreated(ctx, nu, c, client)
		}

		if u, err = s.concurrentlyCreated(ctx, createErr, func() (*entity.User, error) { return s.findByAlipay(ctx, openID) }); err != nil {
			return nil, err
		}
	}

	if err := s.ensureCanAuthenticate(ctx, u, "alipay"); err != nil {
		return nil, err
	}

	return s.signIn(ctx, u, client)
}
