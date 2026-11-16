package sms_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/sms"
)

type redirect struct {
	target *url.URL
}

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host = r.target.Scheme, r.target.Host

	return http.DefaultTransport.RoundTrip(out)
}

func serve(t *testing.T, h http.HandlerFunc) http.RoundTripper {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	require.NoError(t, err)

	return redirect{target: target}
}

func account() *sms.Config {
	return &sms.Config{
		AccessKeyID: "id", AccessKeySecret: "secret", SignName: "签名", TemplateCode: "SMS_1",
		Endpoint: "dysmsapi.aliyuncs.com", Mock: false,
	}
}

func discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestSignMatchesAcs3Reference(t *testing.T) {
	t.Parallel()

	query := map[string]string{
		"PhoneNumbers": "13800138000", "SignName": "签名", "TemplateCode": "SMS_1", "TemplateParam": `{"code":"123456"}`,
	}

	assert.Equal(t,
		"PhoneNumbers=13800138000&SignName=%E7%AD%BE%E5%90%8D&TemplateCode=SMS_1&TemplateParam=%7B%22code%22%3A%22123456%22%7D",
		sms.CanonicalQuery(query))

	headers := sms.Sign(&sms.Request{
		Method: http.MethodPost, Host: "dysmsapi.aliyuncs.com", Action: "SendSms", Version: "2017-05-25", Query: query,
		AccessKeyID: "id", AccessKeySecret: "secret", Date: "2026-01-02T03:04:05Z", Nonce: "abc",
	})

	assert.Equal(t, "ACS3-HMAC-SHA256 Credential=id,SignedHeaders=host;x-acs-action;x-acs-content-sha256;x-acs-date;"+
		"x-acs-signature-nonce;x-acs-version,Signature=46f04adb7ba52825af1198224bc345f6323e3e708a515ce6e494c7c1d17af4e9",
		headers["authorization"])
	assert.Equal(t, "application/json", headers["accept"])
}

func TestSendCallsSendSms(t *testing.T) {
	t.Parallel()

	var got url.Values

	rt := serve(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()

		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "SendSms", r.Header.Get("X-Acs-Action"))
		assert.Contains(t, r.Header.Get("Authorization"), "Credential=id,")

		_ = json.NewEncoder(w).Encode(map[string]string{"Code": "OK", "BizId": "b"})
	})

	require.NoError(t, sms.New(account(), rt, discard()).Send(t.Context(), "13800138000", map[string]string{"code": "123456"}))
	assert.Equal(t, "13800138000", got.Get("PhoneNumbers"))
	assert.Equal(t, `{"code":"123456"}`, got.Get("TemplateParam"))
	assert.Equal(t, "SMS_1", got.Get("TemplateCode"))
}

func TestSendFails(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"rejected", http.StatusOK, `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"limit"}`},
		{"http error", http.StatusBadRequest, `{"Code":"InvalidParameter","Message":"bad"}`},
		{"empty body", http.StatusOK, `null`},
		{"not json", http.StatusOK, `<html>`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			require.Error(t, sms.New(account(), rt, discard()).Send(t.Context(), "13800138000", map[string]string{"code": "1"}))
		})
	}
}

func TestSendWithoutAccount(t *testing.T) {
	t.Parallel()

	mock := account()
	mock.Mock = true
	mock.AccessKeyID = ""
	require.NoError(t, sms.New(mock, nil, discard()).Send(t.Context(), "13800138000", map[string]string{"code": "1"}))

	missing := account()
	missing.AccessKeySecret = ""
	require.EqualError(t, sms.New(missing, nil, discard()).Send(t.Context(), "13800138000", nil), "SMS client not initialized")

	incomplete := account()
	incomplete.TemplateCode = ""
	require.EqualError(t, sms.New(incomplete, nil, discard()).Send(t.Context(), "13800138000", nil), "SMS configuration incomplete")
}

func text(s string) *string { return &s }

func TestSendSMSResults(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		body   string
		want   sms.Result
	}{
		{
			"sent", http.StatusOK, `{"Code":"OK","Message":"OK","RequestId":"r1","BizId":"b1"}`,
			sms.Result{Success: true, RequestID: "r1", BizID: "b1", Code: "OK", Message: text("OK")},
		},
		{
			"rejected", http.StatusOK, `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"limit","RequestId":"r2"}`,
			sms.Result{Success: false, RequestID: "r2", BizID: "", Code: "isv.BUSINESS_LIMIT_CONTROL", Message: text("limit")},
		},
		{
			"rejected without message", http.StatusOK, `{"Code":"isv.X"}`,
			sms.Result{Success: false, RequestID: "", BizID: "", Code: "isv.X", Message: nil},
		},
		{
			"http error", http.StatusBadRequest, `{"Code":"InvalidParameter","Message":"bad","RequestId":"r3"}`,
			sms.Result{Success: false, RequestID: "", BizID: "", Code: "SEND_ERROR", Message: text("InvalidParameter: code: 400, bad request id: r3")},
		},
		{
			"http error without fields", http.StatusForbidden, `{}`,
			sms.Result{Success: false, RequestID: "", BizID: "", Code: "SEND_ERROR", Message: text("undefined: code: 403, undefined request id: undefined")},
		},
		{
			"empty body", http.StatusOK, `null`,
			sms.Result{Success: false, RequestID: "", BizID: "", Code: "EMPTY_RESPONSE", Message: text("SMS response body is empty")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt := serve(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			assert.Equal(t, tc.want, sms.New(account(), rt, discard()).SendSMS(t.Context(), "13800138000", map[string]string{"content": "c"}))
		})
	}
}

func TestSendSMSWithoutAccount(t *testing.T) {
	t.Parallel()

	mock := account()
	mock.Mock = true
	assert.Equal(t, sms.Result{Success: true, RequestID: "mock-request-id", BizID: "mock-biz-id", Code: "OK", Message: text("Mock SMS sent successfully")},
		sms.New(mock, nil, discard()).SendSMS(t.Context(), "13800138000", nil))

	missing := account()
	missing.AccessKeyID = ""
	assert.Equal(t, sms.Result{Success: false, RequestID: "", BizID: "", Code: "CLIENT_NOT_INITIALIZED", Message: text("SMS client not initialized")},
		sms.New(missing, nil, discard()).SendSMS(t.Context(), "13800138000", nil))

	incomplete := account()
	incomplete.SignName = ""
	assert.Equal(t, sms.Result{Success: false, RequestID: "", BizID: "", Code: "CONFIG_ERROR", Message: text("SMS configuration incomplete")},
		sms.New(incomplete, nil, discard()).SendSMS(t.Context(), "13800138000", nil))
}

func TestSendErrorIsTheResultMessage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
		want string
	}{
		{"message", `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"触发分钟级流控Permits:1"}`, "触发分钟级流控Permits:1"},
		{"no message", `{"Code":"isv.X"}`, "SMS send failed"},
		{"empty message", `{"Code":"isv.X","Message":""}`, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rt := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tc.body)) })

			require.EqualError(t, sms.New(account(), rt, discard()).Send(t.Context(), "13800138000", nil), tc.want)
		})
	}
}
