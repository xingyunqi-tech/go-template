package sms_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/sms"
)

func account(t *testing.T, h http.HandlerFunc) *sms.Config {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return &sms.Config{AccessKeyID: "id", AccessKeySecret: "secret", SignName: "签名", TemplateCode: "SMS_1", Endpoint: srv.URL}
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

	cfg := account(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()

		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "SendSms", r.Header.Get("X-Acs-Action"))
		assert.Contains(t, r.Header.Get("Authorization"), "Credential=id,")

		_ = json.NewEncoder(w).Encode(map[string]string{"Code": "OK", "BizId": "b"})
	})

	require.NoError(t, sms.New(cfg, nil).Send(t.Context(), "+8613800138000", "123456"))
	assert.Equal(t, "13800138000", got.Get("PhoneNumbers"))
	assert.Equal(t, `{"code":"123456"}`, got.Get("TemplateParam"))
	assert.Equal(t, "SMS_1", got.Get("TemplateCode"))
	assert.Equal(t, "签名", got.Get("SignName"))
}

func TestNumber(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "13800138000", sms.Number("+8613800138000"))
	assert.Equal(t, "14155552671", sms.Number("+14155552671"))
}

func TestSendFailuresAreBadGateway(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		status  int
		body    string
		notSent bool
	}{
		{"rejected", http.StatusOK, `{"Code":"isv.BUSINESS_LIMIT_CONTROL","Message":"limit"}`, true},
		{"http error", http.StatusBadRequest, `{"Code":"InvalidParameter","Message":"bad"}`, true},
		{"empty body", http.StatusOK, `null`, true},
		{"not json", http.StatusOK, `<html>`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := account(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})

			err := sms.New(cfg, nil).Send(t.Context(), "+8613800138000", "123456")
			require.ErrorIs(t, err, entity.New(entity.KeyBadGateway))
			assert.Equal(t, tc.notSent, errors.Is(err, repo.ErrSMSNotSent))
		})
	}
}

func TestSendUnreachableIsBadGatewayAndNotSent(t *testing.T) {
	t.Parallel()

	cfg := &sms.Config{AccessKeyID: "id", AccessKeySecret: "s", SignName: "n", TemplateCode: "t", Endpoint: "http://127.0.0.1:1"}

	err := sms.New(cfg, nil).Send(t.Context(), "+8613800138000", "123456")
	require.ErrorIs(t, err, entity.New(entity.KeyBadGateway))
	require.ErrorIs(t, err, repo.ErrSMSNotSent)
}

func TestSendTimeoutMayHaveBeenSent(t *testing.T) {
	t.Parallel()

	cfg := account(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := sms.New(cfg, nil).Send(ctx, "+8613800138000", "123456")
	require.ErrorIs(t, err, entity.New(entity.KeyBadGateway))
	assert.NotErrorIs(t, err, repo.ErrSMSNotSent)
}

func TestLogWritesTheCode(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	require.NoError(t, sms.NewLog(slog.New(slog.NewJSONHandler(&buf, nil))).Send(t.Context(), "+8613800138000", "123456"))
	assert.Contains(t, buf.String(), `"verification_code":"123456"`)
}
