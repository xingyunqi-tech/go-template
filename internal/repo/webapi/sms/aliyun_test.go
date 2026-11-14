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
