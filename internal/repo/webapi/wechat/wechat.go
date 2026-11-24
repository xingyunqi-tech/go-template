// Package wechat calls the WeChat sign-in APIs: mini program jscode2session, open platform OAuth
// and the mini program phone number.
package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/outbound"
	"github.com/xingyunqi-tech/go-template/pkg/phone"
)

const (
	// DefaultAPIBase is the WeChat API host.
	DefaultAPIBase = "https://api.weixin.qq.com"
	// AccessTokenCacheKey caches the mini program access token.
	AccessTokenCacheKey = "wechat:miniapp:access_token" //nolint:gosec // cache key, not a credential

	_timeout          = 10 * time.Second
	_maxBody          = 1 << 20
	_tokenMargin      = 300 * time.Second
	_tokenShortExpiry = 600 * time.Second

	_errInvalidCredential = 40001
	_errInvalidToken      = 40014
	_errTokenExpired      = 42001
	_errInvalidCode       = 40029
	_errCodeUsed          = 40163
)

var (
	errAPI     = errors.New("wechat: api error")
	errNoID    = errors.New("wechat: response has no openid")
	errNoPhone = errors.New("wechat: response has no phone number")
	errHTTP    = errors.New("wechat: request failed")
)

// Credentials are the app id and secret of a mini program or an open platform app.
type Credentials struct {
	AppID     string
	AppSecret string
}

// Client calls the WeChat APIs.
type Client struct {
	base  string
	http  *http.Client
	cache repo.Cache
	mu    sync.Mutex
}

// New returns a Client; transport nil uses http.DefaultTransport, base "" is DefaultAPIBase.
func New(base string, cache repo.Cache, transport http.RoundTripper) *Client {
	if base == "" {
		base = DefaultAPIBase
	}

	return &Client{ //nolint:exhaustruct // zero mutex
		base: strings.TrimRight(base, "/"), http: &http.Client{Transport: transport, Timeout: _timeout}, cache: cache, //nolint:exhaustruct // defaults
	}
}

type reply struct {
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
	OpenID      string `json:"openid"`
	UnionID     string `json:"unionid"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	PhoneInfo   *struct {
		PurePhoneNumber string `json:"purePhoneNumber"`
		CountryCode     string `json:"countryCode"`
	} `json:"phone_info"`
}

func (r *reply) err() error {
	return fmt.Errorf("%w %d: %s", errAPI, r.ErrCode, r.ErrMsg)
}

// codeError maps an errcode to auth.providerCodeInvalid for a bad code, else common.badGateway.
func (r *reply) codeError() error {
	if r.ErrCode == _errInvalidCode || r.ErrCode == _errCodeUsed {
		return entity.Wrap(entity.KeyProviderCodeInvalid, r.err())
	}

	return entity.Wrap(entity.KeyBadGateway, r.err())
}

// fetch GETs path, or POSTs body as JSON; transport and HTTP failures are common.badGateway.
func (c *Client) fetch(ctx context.Context, path string, query url.Values, body any) (*reply, error) {
	method, payload := http.MethodGet, io.Reader(http.NoBody)

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("wechat: %w", err)
		}

		method, payload = http.MethodPost, bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path+"?"+query.Encode(), payload)
	if err != nil {
		return nil, fmt.Errorf("wechat: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, entity.Wrap(entity.KeyBadGateway, fmt.Errorf("wechat: %w", outbound.Redact(err)))
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, _maxBody))
	if err != nil {
		return nil, entity.Wrap(entity.KeyBadGateway, fmt.Errorf("wechat: %w", err))
	}

	var out reply
	if err := json.Unmarshal(raw, &out); err != nil || res.StatusCode >= http.StatusBadRequest {
		return nil, entity.Wrap(entity.KeyBadGateway, fmt.Errorf("%w: %s %s: status %d", errHTTP, method, path, res.StatusCode))
	}

	return &out, nil
}

func (c *Client) exchange(ctx context.Context, path string, query url.Values, provider string) (*entity.ExternalIdentity, error) {
	r, err := c.fetch(ctx, path, query, nil)
	if err != nil {
		return nil, err
	}

	if r.ErrCode != 0 {
		return nil, r.codeError()
	}

	if r.OpenID == "" {
		return nil, entity.Wrap(entity.KeyBadGateway, errNoID)
	}

	return &entity.ExternalIdentity{Provider: provider, Subject: r.OpenID, UnionID: r.UnionID}, nil
}

// MiniApp signs in with the code of wx.login.
type MiniApp struct {
	client *Client
	creds  Credentials
}

// NewMiniApp returns the wechat_miniapp provider.
func NewMiniApp(client *Client, creds Credentials) *MiniApp {
	return &MiniApp{client: client, creds: creds}
}

// ID is wechat_miniapp.
func (*MiniApp) ID() string { return entity.ProviderWechatMiniApp }

// Exchange calls jscode2session.
func (m *MiniApp) Exchange(ctx context.Context, code string) (*entity.ExternalIdentity, error) {
	return m.client.exchange(ctx, "/sns/jscode2session", url.Values{
		"appid": {m.creds.AppID}, "secret": {m.creds.AppSecret}, "js_code": {code}, "grant_type": {"authorization_code"},
	}, entity.ProviderWechatMiniApp)
}

// Phone resolves the code of getPhoneNumber to an E.164 number.
func (m *MiniApp) Phone(ctx context.Context, code string) (string, error) {
	r, err := m.callWithToken(ctx, "/wxa/business/getuserphonenumber", map[string]string{"code": code})
	if err != nil {
		return "", err
	}

	if r.ErrCode != 0 {
		return "", r.codeError()
	}

	if r.PhoneInfo == nil || r.PhoneInfo.PurePhoneNumber == "" {
		return "", entity.Wrap(entity.KeyBadGateway, errNoPhone)
	}

	e164, err := phone.Normalize("+" + r.PhoneInfo.CountryCode + r.PhoneInfo.PurePhoneNumber)
	if err != nil {
		return "", entity.Wrap(entity.KeyBadGateway, err)
	}

	return e164, nil
}

func invalidToken(code int) bool {
	return code == _errInvalidCredential || code == _errInvalidToken || code == _errTokenExpired
}

// callWithToken calls a mini program API with the access token and fetches a new token once
// when WeChat reports it invalid.
func (m *MiniApp) callWithToken(ctx context.Context, path string, body any) (*reply, error) {
	token, err := m.accessToken(ctx, "")
	if err != nil {
		return nil, err
	}

	r, err := m.client.fetch(ctx, path, url.Values{"access_token": {token}}, body)
	if err != nil || !invalidToken(r.ErrCode) {
		return r, err
	}

	if token, err = m.accessToken(ctx, token); err != nil {
		return nil, err
	}

	return m.client.fetch(ctx, path, url.Values{"access_token": {token}}, body)
}

// accessToken returns the cached stable token unless it is invalid; one request at a time
// fetches a new one, asking WeChat to force a refresh only when it hands out invalid again.
func (m *MiniApp) accessToken(ctx context.Context, invalid string) (string, error) {
	if token, err := m.cached(ctx, invalid); token != "" || err != nil {
		return token, err
	}

	m.client.mu.Lock()
	defer m.client.mu.Unlock()

	if token, err := m.cached(ctx, invalid); token != "" || err != nil {
		return token, err
	}

	token, err := m.newToken(ctx, false)
	if err != nil || invalid == "" || token != invalid {
		return token, err
	}

	return m.newToken(ctx, true)
}

// cached returns the cached token, "" when there is none or it is invalid.
func (m *MiniApp) cached(ctx context.Context, invalid string) (string, error) {
	var token string

	found, err := m.client.cache.Get(ctx, AccessTokenCacheKey, &token)
	if err != nil {
		return "", entity.Wrap(entity.KeyServiceUnavailable, err)
	}

	if !found || token == invalid {
		return "", nil
	}

	return token, nil
}

// newToken fetches a stable token and caches it until shortly before it expires; the caller
// holds the client lock.
func (m *MiniApp) newToken(ctx context.Context, force bool) (string, error) {
	r, err := m.client.fetch(ctx, "/cgi-bin/stable_token", nil, map[string]any{
		"grant_type": "client_credential", "appid": m.creds.AppID, "secret": m.creds.AppSecret, "force_refresh": force,
	})
	if err != nil {
		return "", err
	}

	if r.ErrCode != 0 || r.AccessToken == "" {
		return "", entity.Wrap(entity.KeyBadGateway, r.err())
	}

	expires := time.Duration(r.ExpiresIn) * time.Second

	ttl := expires / 2 //nolint:mnd // half of a short lifetime
	if expires > _tokenShortExpiry {
		ttl = expires - _tokenMargin
	}

	if ttl > 0 {
		if err := m.client.cache.Set(ctx, AccessTokenCacheKey, r.AccessToken, ttl); err != nil {
			return "", entity.Wrap(entity.KeyServiceUnavailable, err)
		}
	}

	return r.AccessToken, nil
}

// App signs in with the OAuth code of the open platform.
type App struct {
	client *Client
	creds  Credentials
}

// NewApp returns the wechat_app provider.
func NewApp(client *Client, creds Credentials) *App {
	return &App{client: client, creds: creds}
}

// ID is wechat_app.
func (*App) ID() string { return entity.ProviderWechatApp }

// Exchange calls sns/oauth2/access_token.
func (a *App) Exchange(ctx context.Context, code string) (*entity.ExternalIdentity, error) {
	return a.client.exchange(ctx, "/sns/oauth2/access_token", url.Values{
		"appid": {a.creds.AppID}, "secret": {a.creds.AppSecret}, "code": {code}, "grant_type": {"authorization_code"},
	}, entity.ProviderWechatApp)
}
