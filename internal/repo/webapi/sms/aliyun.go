// Package sms sends SMS through Aliyun Dysms SendSms (2017-05-25, ACS3-HMAC-SHA256 signature).
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
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/outbound"
)

const (
	_apiVersion = "2017-05-25"
	_action     = "SendSms"
	_algorithm  = "ACS3-HMAC-SHA256"
	_timeout    = 10 * time.Second
	_nonceBytes = 16
	_dateLayout = "2006-01-02T15:04:05Z"
	_codeOK     = "OK"
	_maxBody    = 1 << 20
)

const (
	_codeNotInitialized = "CLIENT_NOT_INITIALIZED"
	_codeConfigError    = "CONFIG_ERROR"
	_codeEmptyResponse  = "EMPTY_RESPONSE"
	_codeSendError      = "SEND_ERROR"
	_msgNotInitialized  = "SMS client not initialized"
	_msgIncomplete      = "SMS configuration incomplete"
	_msgEmptyBody       = "SMS response body is empty"
	_msgSendFailed      = "SMS send failed"
	_undefined          = "undefined"
)

// sendError is a failed send with the nestjs error message.
type sendError string

func (e sendError) Error() string { return string(e) }

var errEmptyBody error = sendError(_msgEmptyBody)

// Result is the outcome of nestjs AliyunSmsProvider.sendSms; Message is nil when Dysms returned none.
type Result struct {
	Success   bool
	RequestID string
	BizID     string
	Code      string
	Message   *string
}

// Config is the Dysms account; Mock only logs the messages.
type Config struct {
	AccessKeyID     string
	AccessKeySecret string
	SignName        string
	TemplateCode    string
	Endpoint        string
	Mock            bool
}

// Aliyun sends template messages with Dysms.
type Aliyun struct {
	cfg    Config
	client *http.Client
	log    *slog.Logger
	now    func() time.Time
}

// New returns an Aliyun sender; transport nil uses http.DefaultTransport.
func New(cfg *Config, transport http.RoundTripper, log *slog.Logger) *Aliyun {
	return &Aliyun{cfg: *cfg, client: &http.Client{Transport: transport, Timeout: _timeout}, log: log, now: time.Now} //nolint:exhaustruct // defaults
}

func (a *Aliyun) enabled() bool {
	return !a.cfg.Mock && a.cfg.AccessKeyID != "" && a.cfg.AccessKeySecret != ""
}

func templateParam(params map[string]string) string {
	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(params)

	return strings.TrimSuffix(buf.String(), "\n")
}

func text(s string) *string {
	return &s
}

func failed(code, message string) Result {
	return Result{Success: false, RequestID: "", BizID: "", Code: code, Message: text(message)}
}

// Send sends the template with params to phone; the error message is the failure message of
// SendSMS, or "SMS send failed" when there is none.
func (a *Aliyun) Send(ctx context.Context, phone string, params map[string]string) error {
	r := a.SendSMS(ctx, phone, params)

	switch {
	case r.Success:
		return nil
	case r.Message == nil:
		return sendError(_msgSendFailed)
	default:
		return sendError(*r.Message)
	}
}

// SendSMS sends the template with params to phone.
func (a *Aliyun) SendSMS(ctx context.Context, phone string, params map[string]string) Result {
	encoded := templateParam(params)

	switch {
	case a.cfg.Mock:
		a.log.InfoContext(ctx, "[MOCK SMS]", "to", entity.MaskPhone(phone), "params", encoded)

		return Result{Success: true, RequestID: "mock-request-id", BizID: "mock-biz-id", Code: _codeOK, Message: text("Mock SMS sent successfully")}
	case !a.enabled():
		a.log.ErrorContext(ctx, "SMS client not initialized")

		return failed(_codeNotInitialized, _msgNotInitialized)
	case a.cfg.SignName == "" || a.cfg.TemplateCode == "":
		a.log.ErrorContext(ctx, "SMS sign name or template code not configured")

		return failed(_codeConfigError, _msgIncomplete)
	}

	body, err := a.call(ctx, map[string]string{
		"PhoneNumbers": phone, "SignName": a.cfg.SignName, "TemplateCode": a.cfg.TemplateCode, "TemplateParam": encoded,
	})

	switch {
	case errors.Is(err, errEmptyBody):
		a.log.ErrorContext(ctx, "SMS response body is empty")

		return failed(_codeEmptyResponse, _msgEmptyBody)
	case err != nil:
		a.log.ErrorContext(ctx, "SMS send error", "error", err.Error())

		return failed(_codeSendError, err.Error())
	case body.Code != _codeOK:
		a.log.ErrorContext(ctx, "SMS send failed", "code", body.Code, "message", body.Message)

		return Result{Success: false, RequestID: body.RequestID, BizID: "", Code: body.Code, Message: body.Message}
	}

	a.log.InfoContext(ctx, "SMS sent successfully", "to", entity.MaskPhone(phone), "biz_id", body.BizID)

	return Result{Success: true, RequestID: body.RequestID, BizID: body.BizID, Code: body.Code, Message: body.Message}
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
	host := a.cfg.Endpoint
	headers := Sign(&Request{
		Method: http.MethodPost, Host: host, Action: _action, Version: _apiVersion, Query: query,
		AccessKeyID: a.cfg.AccessKeyID, AccessKeySecret: a.cfg.AccessKeySecret,
		Date: a.now().UTC().Format(_dateLayout), Nonce: nonce(),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/?"+CanonicalQuery(query), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("sms: %w", err)
	}

	for k, v := range headers {
		if k == "host" {
			continue
		}

		req.Header.Set(k, v)
	}

	res, err := a.client.Do(req)
	if err != nil {
		return nil, outbound.Redact(err) //nolint:wrapcheck // the message is the recorded failure
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, _maxBody))
	if err != nil {
		return nil, fmt.Errorf("sms: %w", err)
	}

	var body *response
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("sms: %w", err)
	}

	if body == nil {
		return nil, errEmptyBody
	}

	if res.StatusCode >= http.StatusBadRequest {
		return nil, sendError(fmt.Sprintf("%s: code: %d, %s request id: %s",
			orUndefined(body.Code), res.StatusCode, orUndefined(deref(body.Message)), orUndefined(body.RequestID)))
	}

	return body, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func orUndefined(s string) string {
	if s == "" {
		return _undefined
	}

	return s
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
