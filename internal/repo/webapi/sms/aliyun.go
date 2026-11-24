// Package sms sends verification codes through Aliyun Dysms SendSms (2017-05-25,
// ACS3-HMAC-SHA256 signature), or only logs them.
package sms

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/outbound"
)

const (
	_apiVersion   = "2017-05-25"
	_action       = "SendSms"
	_algorithm    = "ACS3-HMAC-SHA256"
	_timeout      = 10 * time.Second
	_nonceBytes   = 16
	_dateLayout   = "2006-01-02T15:04:05Z"
	_codeOK       = "OK"
	_maxBody      = 1 << 20
	_mainlandCode = "+86"
)

var errRejected = errors.New("sms: rejected")

// Config is the Dysms account; Endpoint is the API base URL.
type Config struct {
	AccessKeyID     string
	AccessKeySecret string
	SignName        string
	TemplateCode    string
	Endpoint        string
}

// Aliyun sends the code template with Dysms.
type Aliyun struct {
	cfg    Config
	client *http.Client
	now    func() time.Time
}

var _ repo.SMSSender = (*Aliyun)(nil)

// New returns an Aliyun sender; transport nil uses http.DefaultTransport.
func New(cfg *Config, transport http.RoundTripper) *Aliyun {
	return &Aliyun{cfg: *cfg, client: &http.Client{Transport: transport, Timeout: _timeout}, now: time.Now} //nolint:exhaustruct // defaults
}

func templateParam(params map[string]string) string {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(params)

	return strings.TrimSuffix(buf.String(), "\n")
}

// Number is the PhoneNumbers form of an E.164 number: mainland numbers without +86, others
// without +.
func Number(e164 string) string {
	if rest, ok := strings.CutPrefix(e164, _mainlandCode); ok {
		return rest
	}

	return strings.TrimPrefix(e164, "+")
}

// Send implements repo.SMSSender; every failure is common.badGateway, marked repo.ErrSMSNotSent
// when Dysms refused the message or the connection was never made.
func (a *Aliyun) Send(ctx context.Context, phone, code string) error {
	body, err := a.call(ctx, map[string]string{
		"PhoneNumbers": Number(phone), "SignName": a.cfg.SignName, "TemplateCode": a.cfg.TemplateCode,
		"TemplateParam": templateParam(map[string]string{"code": code}),
	})
	if err != nil {
		return entity.Wrap(entity.KeyBadGateway, err)
	}

	if body.Code != _codeOK {
		return entity.Wrap(entity.KeyBadGateway, fmt.Errorf("%w: %w: %s %s", repo.ErrSMSNotSent, errRejected, body.Code, deref(body.Message)))
	}

	return nil
}

// Log only logs the codes, for development without an SMS account.
type Log struct {
	log *slog.Logger
}

var _ repo.SMSSender = (*Log)(nil)

// NewLog returns a Log.
func NewLog(log *slog.Logger) *Log {
	return &Log{log: log}
}

// Send implements repo.SMSSender.
func (l *Log) Send(ctx context.Context, phone, code string) error {
	l.log.InfoContext(ctx, "sms not sent: SMS_PROVIDER=log", "phone", phone, "verification_code", code)

	return nil
}

type response struct {
	Code      string  `json:"Code"`
	Message   *string `json:"Message"`
	RequestID string  `json:"RequestId"`
	BizID     string  `json:"BizId"`
}

func nonce() string {
	b := make([]byte, _nonceBytes)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

func (a *Aliyun) call(ctx context.Context, query map[string]string) (*response, error) {
	base, err := url.Parse(a.cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("sms: endpoint: %w", err)
	}

	headers := Sign(&Request{
		Method: http.MethodPost, Host: base.Host, Action: _action, Version: _apiVersion, Query: query,
		AccessKeyID: a.cfg.AccessKeyID, AccessKeySecret: a.cfg.AccessKeySecret,
		Date: a.now().UTC().Format(_dateLayout), Nonce: nonce(),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.cfg.Endpoint, "/")+"/?"+CanonicalQuery(query), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("sms: %w", err)
	}

	for k, v := range headers {
		if k != "host" {
			req.Header.Set(k, v)
		}
	}

	res, err := a.client.Do(req)
	if err != nil {
		if opErr := (*net.OpError)(nil); errors.As(err, &opErr) && opErr.Op == "dial" {
			return nil, fmt.Errorf("%w: %w", repo.ErrSMSNotSent, outbound.Redact(err))
		}

		return nil, fmt.Errorf("sms: %w", outbound.Redact(err))
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, _maxBody))
	if err != nil {
		return nil, fmt.Errorf("sms: %w", err)
	}

	var body response
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("sms: status %d: %w", res.StatusCode, err)
	}

	return &body, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// Request is what the ACS3 signature covers; the body is empty.
type Request struct {
	Method          string
	Host            string
	Action          string
	Version         string
	Query           map[string]string
	AccessKeyID     string
	AccessKeySecret string
	Date            string
	Nonce           string
}

func encode(s string) string {
	var b strings.Builder

	for _, c := range []byte(s) {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-_.~", c) >= 0 {
			b.WriteByte(c)

			continue
		}

		fmt.Fprintf(&b, "%%%02X", c)
	}

	return b.String()
}

// CanonicalQuery is the query sorted by key with RFC 3986 encoding.
func CanonicalQuery(query map[string]string) string {
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = encode(k) + "=" + encode(query[k])
	}

	return strings.Join(parts, "&")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}

// Sign returns the request headers with the ACS3-HMAC-SHA256 authorization.
func Sign(r *Request) map[string]string {
	headers := map[string]string{
		"host":                  r.Host,
		"x-acs-action":          r.Action,
		"x-acs-content-sha256":  sha256Hex(nil),
		"x-acs-date":            r.Date,
		"x-acs-signature-nonce": r.Nonce,
		"x-acs-version":         r.Version,
	}

	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}

	slices.Sort(names)

	var canonicalHeaders strings.Builder
	for _, n := range names {
		canonicalHeaders.WriteString(n + ":" + strings.TrimSpace(headers[n]) + "\n")
	}

	signedHeaders := strings.Join(names, ";")
	canonical := strings.Join([]string{
		r.Method, "/", CanonicalQuery(r.Query), canonicalHeaders.String(), signedHeaders, headers["x-acs-content-sha256"],
	}, "\n")

	mac := hmac.New(sha256.New, []byte(r.AccessKeySecret))
	_, _ = mac.Write([]byte(_algorithm + "\n" + sha256Hex([]byte(canonical))))

	headers["accept"] = "application/json"
	headers["authorization"] = _algorithm + " Credential=" + r.AccessKeyID + ",SignedHeaders=" + signedHeaders +
		",Signature=" + hex.EncodeToString(mac.Sum(nil))

	return headers
}
