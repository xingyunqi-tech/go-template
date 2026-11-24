// Package alipay signs in with alipay.system.oauth.token: requests are signed with RSA2 and
// responses verified with the Alipay public key.
package alipay

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/outbound"
)

const (
	// DefaultGateway is the production gateway.
	DefaultGateway = "https://openapi.alipay.com/gateway.do"

	_timeout       = 10 * time.Second
	_maxBody       = 1 << 20
	_timeLayout    = "2006-01-02 15:04:05"
	_beijingOffset = 8 * 60 * 60
	_invalidCode   = "isv.code-"
	_tokenNode     = "alipay_system_oauth_token_response"
	_errorNode     = "error_response"
)

var (
	errNotRSA      = errors.New("alipay: key is not RSA")
	errNoKey       = errors.New("alipay: key holds no PEM block or base64 DER")
	errSignature   = errors.New("alipay: response signature does not verify")
	errNoSubject   = errors.New("alipay: response has no user_id or open_id")
	errUnsupported = errors.New("alipay: response holds neither the token nor an error")
)

// Config holds the app id, its RSA private key, the Alipay public key and the gateway URL.
type Config struct {
	AppID      string
	PrivateKey string
	PublicKey  string
	Gateway    string
}

// Client is the alipay provider.
type Client struct {
	appID   string
	gateway string
	private *rsa.PrivateKey
	public  *rsa.PublicKey
	http    *http.Client
	now     func() time.Time
}

// New parses the keys; transport nil uses http.DefaultTransport, an empty gateway is
// DefaultGateway.
func New(cfg *Config, transport http.RoundTripper) (*Client, error) {
	private, err := parsePrivate(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("ALIPAY_PRIVATE_KEY: %w", err)
	}

	public, err := parsePublic(cfg.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("ALIPAY_PUBLIC_KEY: %w", err)
	}

	gateway := cfg.Gateway
	if gateway == "" {
		gateway = DefaultGateway
	}

	return &Client{
		appID: cfg.AppID, gateway: gateway, private: private, public: public,
		http: &http.Client{Transport: transport, Timeout: _timeout}, now: time.Now, //nolint:exhaustruct // defaults
	}, nil
}

// ID is alipay.
func (*Client) ID() string { return entity.ProviderAlipay }

// BeijingTime formats t in UTC+8 like the gateway expects.
func BeijingTime(t time.Time) string {
	return t.In(time.FixedZone("CST", _beijingOffset)).Format(_timeLayout)
}

// SignString joins the non-empty parameters sorted by key as key=value pairs.
func SignString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if v != "" {
			keys = append(keys, k)
		}
	}

	slices.Sort(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + params[k]
	}

	return strings.Join(parts, "&")
}

// der decodes a PEM block or bare base64.
func der(raw string) ([]byte, error) {
	if block, _ := pem.Decode([]byte(raw)); block != nil {
		return block.Bytes, nil
	}

	b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(raw), ""))
	if err != nil {
		return nil, errNoKey
	}

	return b, nil
}

func parsePrivate(raw string) (*rsa.PrivateKey, error) {
	b, err := der(raw)
	if err != nil {
		return nil, err
	}

	if key, err := x509.ParsePKCS8PrivateKey(b); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errNotRSA
		}

		return rsaKey, nil
	}

	key, err := x509.ParsePKCS1PrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", err)
	}

	return key, nil
}

func parsePublic(raw string) (*rsa.PublicKey, error) {
	b, err := der(raw)
	if err != nil {
		return nil, err
	}

	key, err := x509.ParsePKIXPublicKey(b)
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", err)
	}

	rsaKey, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errNotRSA
	}

	return rsaKey, nil
}

// Sign signs the parameters with RSA2 (SHA-256).
func (c *Client) Sign(params map[string]string) (string, error) {
	sum := sha256.Sum256([]byte(SignString(params)))

	sig, err := rsa.SignPKCS1v15(nil, c.private, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("alipay: sign: %w", err)
	}

	return base64.StdEncoding.EncodeToString(sig), nil
}

// verify checks sign over the raw JSON of a response node.
func (c *Client) verify(node json.RawMessage, sign string) error {
	sig, err := base64.StdEncoding.DecodeString(sign)
	if err != nil {
		return errSignature
	}

	sum := sha256.Sum256(node)
	if rsa.VerifyPKCS1v15(c.public, crypto.SHA256, sum[:], sig) != nil {
		return errSignature
	}

	return nil
}

type gatewayError struct {
	Code    string `json:"code"`
	Msg     string `json:"msg"`
	SubCode string `json:"sub_code"`
}

type oauthToken struct {
	UserID string `json:"user_id"`
	OpenID string `json:"open_id"`
}

func (c *Client) send(ctx context.Context, params map[string]string) (map[string]json.RawMessage, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gateway, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", outbound.Redact(err))
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, _maxBody))
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", err)
	}

	var out map[string]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("alipay: status %d: %w", res.StatusCode, err)
	}

	return out, nil
}

// Exchange trades an auth_code for the user: open_id when the app uses open ids, else user_id.
// A rejected code is auth.providerCodeInvalid; other failures, including a token response
// whose signature does not verify, are common.badGateway.
func (c *Client) Exchange(ctx context.Context, code string) (*entity.ExternalIdentity, error) {
	params := map[string]string{
		"app_id": c.appID, "method": "alipay.system.oauth.token", "format": "JSON", "charset": "utf-8",
		"sign_type": "RSA2", "timestamp": BeijingTime(c.now()), "version": "1.0",
		"grant_type": "authorization_code", "code": code,
	}

	sign, err := c.Sign(params)
	if err != nil {
		return nil, entity.Wrap(entity.KeyInternalError, err)
	}

	params["sign"] = sign

	reply, err := c.send(ctx, params)
	if err != nil {
		return nil, entity.Wrap(entity.KeyBadGateway, err)
	}

	if node, ok := reply[_errorNode]; ok {
		var e gatewayError

		_ = json.Unmarshal(node, &e)
		cause := fmt.Errorf("alipay: %s %s %s", e.Code, e.SubCode, e.Msg) //nolint:err113 // gateway message

		if strings.HasPrefix(e.SubCode, _invalidCode) {
			return nil, entity.Wrap(entity.KeyProviderCodeInvalid, cause)
		}

		return nil, entity.Wrap(entity.KeyBadGateway, cause)
	}

	node, ok := reply[_tokenNode]
	if !ok {
		return nil, entity.Wrap(entity.KeyBadGateway, errUnsupported)
	}

	return c.identity(node, reply["sign"])
}

func (c *Client) identity(node, rawSign json.RawMessage) (*entity.ExternalIdentity, error) {
	var sign string

	_ = json.Unmarshal(rawSign, &sign)

	if err := c.verify(node, sign); err != nil {
		return nil, entity.Wrap(entity.KeyBadGateway, err)
	}

	var token oauthToken
	if err := json.Unmarshal(node, &token); err != nil {
		return nil, entity.Wrap(entity.KeyBadGateway, fmt.Errorf("alipay: %w", err))
	}

	subject := token.OpenID
	if subject == "" {
		subject = token.UserID
	}

	if subject == "" {
		return nil, entity.Wrap(entity.KeyBadGateway, errNoSubject)
	}

	return &entity.ExternalIdentity{Provider: entity.ProviderAlipay, Subject: subject, UnionID: ""}, nil
}
