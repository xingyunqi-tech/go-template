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
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/outbound"
)

const (
	// APIBase is the WeChat API host.
	APIBase = "https://api.weixin.qq.com"
	// AccessTokenCacheKey caches the mini program access token.
	AccessTokenCacheKey = "wechat:mini_app:access_token" //nolint:gosec // cache key, not a credential

	_timeout          = 10 * time.Second
	_maxBody          = 1 << 20
	_tokenMargin      = 300
	_tokenShortExpiry = 600

	_errInvalidCredential = 40001
	_errInvalidToken      = 40014
	_errTokenExpired      = 42001
	_errInvalidCode       = 40029
	_errCodeUsed          = 40163
)

var errAPI = errors.New("wechat: api error")

// Config holds the mini program and open platform credentials.
type Config struct {
	MiniAppID     string
	MiniAppSecret string
	AppID         string
	AppSecret     string
}

// Client calls the WeChat APIs.
type Client struct {
	cfg     Config
	http    *http.Client
	cache   repo.Cache
	log     *slog.Logger
	tokenMu sync.Mutex
}

// New returns a Client; transport nil uses http.DefaultTransport.
func New(cfg *Config, cache repo.Cache, transport http.RoundTripper, log *slog.Logger) *Client {
	return &Client{ //nolint:exhaustruct // zero mutex
		cfg: *cfg, http: &http.Client{Transport: transport, Timeout: _timeout}, cache: cache, log: log, //nolint:exhaustruct // defaults
	}
}

func failure(field, key string, wechatError any) *entity.Error {
	fields := []entity.Detail{{Name: field, Value: key}}
	if wechatError != nil {
		fields = append(fields, entity.Detail{Name: "wechatError", Value: wechatError})
	}

	return entity.InvalidFields(fields...)
}

func notConfigured() *entity.Error {
	return failure("wechat", "notConfigured", nil)
}

func requestFailed() *entity.Error {
	return failure("wechat", "requestFailed", nil)
}

// apiError is a non-zero errcode.
type apiError struct {
	code int
	msg  any
}

func (e *apiError) Error() string {
	return fmt.Sprintf("WeChat API error %d: %v", e.code, e.msg)
}

func (e *apiError) Unwrap() error {
	return errAPI
}

// reply is a decoded response body.
type reply map[string]any

func (r reply) errcode() int {
	switch v := r["errcode"].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)

		return n
	default:
		return 0
	}
}

func (r reply) str(key string) *string {
	switch v := r[key].(type) {
	case string:
		return &v
	case nil:
		return nil
	default:
		s := fmt.Sprint(v)

		return &s
	}
}

func (r reply) text(key string) string {
	if s := r.str(key); s != nil {
		return *s
	}

	return ""
}

func newRequest(ctx context.Context, path string, query url.Values, body any) (*http.Request, error) {
	target := APIBase + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	if body == nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("wechat: %w", err)
		}

		return req, nil
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("wechat: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("wechat: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

// fetch GETs path, or POSTs body as JSON; a non-2xx status is wechat requestFailed.
func (c *Client) fetch(ctx context.Context, path string, query url.Values, body any, label string) (reply, error) {
	req, err := newRequest(ctx, path, query, body)
	if err != nil {
		return nil, err
	}

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wechat: %w", outbound.Redact(err))
	}
	defer res.Body.Close()

	if res.StatusCode >= http.StatusBadRequest {
		c.log.ErrorContext(ctx, "WeChat HTTP error", "api", label, "status", res.StatusCode)

		return nil, requestFailed()
	}

	raw, err := io.ReadAll(io.LimitReader(res.Body, _maxBody))
	if err != nil {
		return nil, fmt.Errorf("wechat: %w", err)
	}

	var out reply
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("wechat: %w", err)
	}

	if out == nil {
		out = reply{}
	}

	return out, nil
}

// passThrough keeps 422 responses and turns any other failure into wechat requestFailed.
func passThrough(err error) error {
	var known *entity.Error
	if errors.As(err, &known) {
		return known
	}

	return requestFailed()
}

// MiniAppLogin exchanges a wx.login code with jscode2session.
func (c *Client) MiniAppLogin(ctx context.Context, code string) (*entity.WechatUser, error) {
	if c.cfg.MiniAppID == "" || c.cfg.MiniAppSecret == "" {
		c.log.ErrorContext(ctx, "WeChat Mini App credentials not configured")

		return nil, notConfigured()
	}

	data, err := c.fetch(ctx, "/sns/jscode2session", url.Values{
		"appid": {c.cfg.MiniAppID}, "secret": {c.cfg.MiniAppSecret}, "js_code": {code}, "grant_type": {"authorization_code"},
	}, nil, "WeChat Mini App login")
	if err != nil {
		c.log.ErrorContext(ctx, "WeChat Mini App login request failed", "error", err.Error())

		return nil, passThrough(err)
	}

	if data.errcode() != 0 {
		c.log.ErrorContext(ctx, "WeChat Mini App login failed", "errcode", data.errcode(), "errmsg", data["errmsg"])

		return nil, failure("code", "invalidCode", data["errmsg"])
	}

	return &entity.WechatUser{OpenID: data.text("openid"), UnionID: data.str("unionid"), Nickname: nil, Gender: nil}, nil
}

// AppLogin exchanges an open platform OAuth code and reads the user profile.
func (c *Client) AppLogin(ctx context.Context, code string) (*entity.WechatUser, error) {
	if c.cfg.AppID == "" || c.cfg.AppSecret == "" {
		c.log.ErrorContext(ctx, "WeChat App credentials not configured")

		return nil, notConfigured()
	}

	u, err := c.appLogin(ctx, code)
	if err != nil {
		c.log.ErrorContext(ctx, "WeChat App login request failed", "error", err.Error())

		return nil, passThrough(err)
	}

	return u, nil
}

func (c *Client) appLogin(ctx context.Context, code string) (*entity.WechatUser, error) {
	token, err := c.fetch(ctx, "/sns/oauth2/access_token", url.Values{
		"appid": {c.cfg.AppID}, "secret": {c.cfg.AppSecret}, "code": {code}, "grant_type": {"authorization_code"},
	}, nil, "WeChat App token")
	if err != nil {
		return nil, err
	}

	if token.errcode() != 0 {
		return nil, failure("code", "invalidCode", token["errmsg"])
	}

	info, err := c.fetch(ctx, "/sns/userinfo", url.Values{
		"access_token": {token.text("access_token")}, "openid": {token.text("openid")},
	}, nil, "WeChat App user info")
	if err != nil {
		return nil, err
	}

	if info.errcode() != 0 {
		return nil, failure("wechat", "userInfoFailed", info["errmsg"])
	}

	unionID := info.str("unionid")
	if unionID == nil {
		unionID = token.str("unionid")
	}

	u := &entity.WechatUser{OpenID: token.text("openid"), UnionID: unionID, Nickname: info.str("nickname"), Gender: nil}
	if sex, ok := info["sex"].(float64); ok {
		g := int(sex)
		u.Gender = &g
	}

	return u, nil
}

// PhoneNumber resolves a getPhoneNumber code.
func (c *Client) PhoneNumber(ctx context.Context, code string) (*entity.WechatPhone, error) {
	data, err := c.callMiniApp(ctx, "/wxa/business/getuserphonenumber", map[string]string{"code": code}, "Get phone number")
	if err != nil {
		return nil, c.phoneError(ctx, err)
	}

	info, _ := data["phone_info"].(map[string]any)
	number, _ := info["purePhoneNumber"].(string)

	if number == "" {
		return nil, c.phoneError(ctx, &apiError{code: -1, msg: "phone_info missing"})
	}

	country := ""
	if v, ok := info["countryCode"]; ok && v != nil {
		country = fmt.Sprint(v)
	}

	return &entity.WechatPhone{Number: number, CountryCode: country}, nil
}

func (c *Client) phoneError(ctx context.Context, err error) error {
	var known *entity.Error
	if errors.As(err, &known) {
		return known
	}

	c.log.ErrorContext(ctx, "Get phone number failed", "error", err.Error())

	var api *apiError
	if errors.As(err, &api) && (api.code == _errInvalidCode || api.code == _errCodeUsed) {
		return entity.UnprocessableKey("invalidPhoneCode", entity.Detail{Name: "phoneCode", Value: "invalidCode"})
	}

	return requestFailed()
}

func invalidToken(code int) bool {
	return code == _errInvalidCredential || code == _errInvalidToken || code == _errTokenExpired
}

// callMiniApp calls a mini program API with the access token, refreshing it once when WeChat
// reports it invalid.
func (c *Client) callMiniApp(ctx context.Context, path string, body any, label string) (reply, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	data, err := c.fetch(ctx, path, url.Values{"access_token": {token}}, body, label)
	if err != nil {
		return nil, err
	}

	if invalidToken(data.errcode()) {
		c.log.WarnContext(ctx, "WeChat access_token invalid, refreshing", "api", label, "errcode", data.errcode())

		if token, err = c.refreshAccessToken(ctx, token); err != nil {
			return nil, err
		}

		if data, err = c.fetch(ctx, path, url.Values{"access_token": {token}}, body, label); err != nil {
			return nil, err
		}
	}

	if code := data.errcode(); code != 0 {
		return nil, &apiError{code: code, msg: data["errmsg"]}
	}

	return data, nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	var cached string

	found, err := c.cache.Get(ctx, AccessTokenCacheKey, &cached)
	if err != nil {
		return "", fmt.Errorf("wechat: %w", err)
	}

	if found && cached != "" {
		return cached, nil
	}

	return c.stableToken(ctx, false)
}

func (c *Client) refreshAccessToken(ctx context.Context, invalid string) (string, error) {
	if err := c.cache.Delete(ctx, AccessTokenCacheKey); err != nil {
		return "", fmt.Errorf("wechat: %w", err)
	}

	token, err := c.stableToken(ctx, false)
	if err != nil || token != invalid {
		return token, err
	}

	return c.stableToken(ctx, true)
}

func (c *Client) stableToken(ctx context.Context, force bool) (string, error) {
	if c.cfg.MiniAppID == "" || c.cfg.MiniAppSecret == "" {
		c.log.ErrorContext(ctx, "WeChat Mini App credentials not configured")

		return "", notConfigured()
	}

	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	data, err := c.fetch(ctx, "/cgi-bin/stable_token", nil, map[string]any{
		"grant_type": "client_credential", "appid": c.cfg.MiniAppID, "secret": c.cfg.MiniAppSecret, "force_refresh": force,
	}, "Get stable_token")
	if err != nil {
		return "", err
	}

	if code := data.errcode(); code != 0 {
		c.log.ErrorContext(ctx, "Get stable_token failed", "errcode", code, "errmsg", data["errmsg"])

		return "", &apiError{code: code, msg: data["errmsg"]}
	}

	expires := 0
	if v, ok := data["expires_in"].(float64); ok {
		expires = int(v)
	}

	ttl := expires / 2 //nolint:mnd // half of a short lifetime
	if expires > _tokenShortExpiry {
		ttl = expires - _tokenMargin
	}

	token := data.text("access_token")
	if ttl > 0 {
		if err := c.cache.Set(ctx, AccessTokenCacheKey, token, time.Duration(ttl)*time.Second); err != nil {
			return "", fmt.Errorf("wechat: %w", err)
		}
	}

	return token, nil
}
