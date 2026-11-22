// Package alipay exchanges mini program auth codes for open ids with alipay.system.oauth.token.
package alipay

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // ALIPAY_SIGN_TYPE=RSA signs with SHA-1
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/outbound"
)

const (
	_timeout       = 30 * time.Second
	_maxBody       = 1 << 20
	_pemLineLength = 64
	_signTypeRSA   = "RSA"
	_timeLayout    = "2006-01-02 15:04:05"
	_beijingOffset = 8 * 60 * 60
	_invalidCode   = "isv.code-"
)

var (
	errNotRSA     = errors.New("ALIPAY_PRIVATE_KEY must be an RSA private key")
	errNoPEM      = errors.New("ALIPAY_PRIVATE_KEY holds no PEM block")
	errHTTPStatus = errors.New("alipay: request failed")
)

// Config holds what alipay.system.oauth.token needs.
type Config struct {
	AppID      string
	PrivateKey string
	Gateway    string
	SignType   string
	Charset    string
}

// Client calls the Alipay open API gateway.
type Client struct {
	cfg  Config
	http *http.Client
	log  *slog.Logger
	now  func() time.Time
}

// New returns a Client; transport nil uses http.DefaultTransport.
func New(cfg *Config, transport http.RoundTripper, log *slog.Logger) *Client {
	return &Client{cfg: *cfg, http: &http.Client{Transport: transport, Timeout: _timeout}, log: log, now: time.Now} //nolint:exhaustruct // defaults
}

func failure(key string) *entity.NestError {
	return entity.NestInvalid("alipay", key)
}

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

// FormatPrivateKey wraps a bare base64 key in a PKCS#8 PEM block.
func FormatPrivateKey(key string) string {
	if strings.Contains(key, "BEGIN PRIVATE KEY") {
		return key
	}

	body := strings.Join(strings.Fields(key), "")

	var b strings.Builder

	b.WriteString("-----BEGIN PRIVATE KEY-----\n") // gitleaks:allow PEM header, no key material

	for len(body) > _pemLineLength {
		b.WriteString(body[:_pemLineLength] + "\n")
		body = body[_pemLineLength:]
	}

	b.WriteString(body + "\n-----END PRIVATE KEY-----")

	return b.String()
}

func parseKey(raw string) (*rsa.PrivateKey, error) {
	if !strings.Contains(raw, "-----BEGIN") {
		raw = FormatPrivateKey(raw)
	}

	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errNoPEM
	}

	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errNotRSA
		}

		return rsaKey, nil
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("alipay: private key: %w", err)
	}

	return key, nil
}

// Sign signs the parameters with RSA2 (SHA-256) or RSA (SHA-1).
func (c *Client) Sign(params map[string]string) (string, error) {
	key, err := parseKey(c.cfg.PrivateKey)
	if err != nil {
		return "", err
	}

	data := []byte(SignString(params))

	var (
		hash   crypto.Hash
		digest []byte
	)

	if c.cfg.SignType == _signTypeRSA {
		sum := sha1.Sum(data) //nolint:gosec // see import
		hash, digest = crypto.SHA1, sum[:]
	} else {
		sum := sha256.Sum256(data)
		hash, digest = crypto.SHA256, sum[:]
	}

	sig, err := rsa.SignPKCS1v15(nil, key, hash, digest)
	if err != nil {
		return "", fmt.Errorf("alipay: sign: %w", err)
	}

	return base64.StdEncoding.EncodeToString(sig), nil
}

type gatewayError struct {
	Code    string `json:"code"`
	SubCode string `json:"sub_code"`
}

type oauthToken struct {
	OpenID string `json:"open_id"`
}

type gatewayReply struct {
	Error *gatewayError `json:"error_response"`
	Token *oauthToken   `json:"alipay_system_oauth_token_response"`
}

func (c *Client) send(ctx context.Context, params map[string]string) (*gatewayReply, error) {
	query := url.Values{}
	for k, v := range params {
		query.Set(k, v)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Gateway+"?"+query.Encode(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", outbound.Redact(err))
	}
	defer res.Body.Close()

	if res.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%w: %s", errHTTPStatus, res.Status)
	}

	raw, err := io.ReadAll(io.LimitReader(res.Body, _maxBody))
	if err != nil {
		return nil, fmt.Errorf("alipay: %w", err)
	}

	var reply gatewayReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("alipay: %w", err)
	}

	return &reply, nil
}

// ExchangeAuthCode returns the open id of an auth code.
func (c *Client) ExchangeAuthCode(ctx context.Context, authCode string) (string, error) {
	if c.cfg.AppID == "" || c.cfg.PrivateKey == "" {
		c.log.ErrorContext(ctx, "Alipay credentials not configured")

		return "", failure("notConfigured")
	}

	params := map[string]string{
		"app_id": c.cfg.AppID, "method": "alipay.system.oauth.token", "format": "JSON", "charset": c.cfg.Charset,
		"sign_type": c.cfg.SignType, "timestamp": BeijingTime(c.now()), "version": "1.0",
		"grant_type": "authorization_code", "code": authCode,
	}

	sign, err := c.Sign(params)
	if err != nil {
		return "", err
	}

	params["sign"] = sign

	reply, err := c.send(ctx, params)
	if err != nil {
		c.log.ErrorContext(ctx, "Alipay oauth token request failed", "error", err.Error())

		return "", failure("requestFailed")
	}

	if reply.Error != nil {
		c.log.ErrorContext(ctx, "Alipay oauth token failed", "code", reply.Error.Code, "sub_code", reply.Error.SubCode)

		if strings.HasPrefix(reply.Error.SubCode, _invalidCode) {
			return "", entity.NestUnprocessable("invalidAlipayAuthCode", entity.Detail{Name: "authCode", Value: "invalidCode"})
		}

		return "", failure("requestFailed")
	}

	if reply.Token == nil || reply.Token.OpenID == "" {
		c.log.ErrorContext(ctx, "Alipay oauth token response has no open_id, check that OpenID is enabled for the app")

		return "", failure("requestFailed")
	}

	return reply.Token.OpenID, nil
}
