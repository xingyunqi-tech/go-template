package alipay_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/alipay"
)

const _keyBits = 2048

func newKey(t *testing.T) (key *rsa.PrivateKey, encoded string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, _keyBits)
	require.NoError(t, err)

	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	encoded = base64.StdEncoding.EncodeToString(der)

	return key, encoded
}

func gateway(t *testing.T, h http.HandlerFunc) string {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv.URL + "/gateway.do"
}

func config(gatewayURL, privateKey string) *alipay.Config {
	return &alipay.Config{AppID: "2021", PrivateKey: privateKey, Gateway: gatewayURL, SignType: "RSA2", Charset: "utf-8"}
}

func TestHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a=1&b=2", alipay.SignString(map[string]string{"b": "2", "a": "1", "c": ""}))
	assert.Equal(t, "2026-01-01 08:00:00", alipay.BeijingTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))

	pemKey := alipay.FormatPrivateKey(strings.Repeat("A", 70))
	assert.Equal(t, "-----BEGIN PRIVATE KEY-----\n"+strings.Repeat("A", 64)+"\nAAAAAA\n-----END PRIVATE KEY-----", pemKey)
	assert.Equal(t, pemKey, alipay.FormatPrivateKey(pemKey))
}

func TestExchangeAuthCodeSignsRequest(t *testing.T) {
	t.Parallel()

	key, encoded := newKey(t)

	endpoint := gateway(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "alipay.system.oauth.token", q.Get("method"))
		assert.Equal(t, "auth-1", q.Get("code"))

		params := map[string]string{}

		for k := range q {
			if k != "sign" {
				params[k] = q.Get(k)
			}
		}

		sig, err := base64.StdEncoding.DecodeString(q.Get("sign"))
		assert.NoError(t, err)

		sum := sha256.Sum256([]byte(alipay.SignString(params)))
		assert.NoError(t, rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig))

		_ = json.NewEncoder(w).Encode(map[string]any{"alipay_system_oauth_token_response": map[string]any{"open_id": "ali-1"}})
	})

	openID, err := alipay.New(config(endpoint, encoded), nil, slog.New(slog.DiscardHandler)).ExchangeAuthCode(t.Context(), "auth-1")
	require.NoError(t, err)
	assert.Equal(t, "ali-1", openID)
}

func TestSignWithPKCS1AndSHA1(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, _keyBits)
	require.NoError(t, err)

	block := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: nil, Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cfg := config("", string(block))
	cfg.SignType = "RSA"

	sig, err := alipay.New(cfg, nil, slog.New(slog.DiscardHandler)).Sign(map[string]string{"a": "1"})
	require.NoError(t, err)
	assert.NotEmpty(t, sig)

	_, err = alipay.New(config("", "not a key"), nil, slog.New(slog.DiscardHandler)).Sign(map[string]string{"a": "1"})
	require.Error(t, err)
}

func TestExchangeAuthCodeErrors(t *testing.T) {
	t.Parallel()

	_, encoded := newKey(t)

	cases := []struct {
		name   string
		status int
		body   any
		want   *entity.NestError
	}{
		{
			"expired code", http.StatusOK,
			map[string]any{"error_response": map[string]any{"code": "40002", "sub_code": "isv.code-invalid"}},
			entity.NestUnprocessable("invalidAlipayAuthCode", entity.Detail{Name: "authCode", Value: "invalidCode"}),
		},
		{
			"other error", http.StatusOK,
			map[string]any{"error_response": map[string]any{"code": "40004", "sub_code": "isv.app-unbind"}},
			entity.NestInvalid("alipay", "requestFailed"),
		},
		{"no open id", http.StatusOK, map[string]any{"alipay_system_oauth_token_response": map[string]any{"user_id": "2088"}}, entity.NestInvalid("alipay", "requestFailed")},
		{"http error", http.StatusBadGateway, map[string]any{}, entity.NestInvalid("alipay", "requestFailed")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			endpoint := gateway(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.body)
			})

			_, err := alipay.New(config(endpoint, encoded), nil, slog.New(slog.DiscardHandler)).ExchangeAuthCode(t.Context(), "x")
			assert.Equal(t, tc.want, err)
		})
	}

	_, err := alipay.New(config("http://unused", ""), nil, slog.New(slog.DiscardHandler)).ExchangeAuthCode(t.Context(), "x")
	assert.Equal(t, entity.NestInvalid("alipay", "notConfigured"), err)

	unreachable := &url.URL{Scheme: "http", Host: "127.0.0.1:1", Path: "/gateway.do"}
	_, err = alipay.New(config(unreachable.String(), encoded), nil, slog.New(slog.DiscardHandler)).ExchangeAuthCode(t.Context(), "x")
	assert.Equal(t, entity.NestInvalid("alipay", "requestFailed"), err)
}
