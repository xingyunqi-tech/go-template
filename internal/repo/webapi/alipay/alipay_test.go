package alipay_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/alipay"
)

const _keyBits = 2048

type keys struct {
	app        *rsa.PrivateKey
	appBase64  string
	gate       *rsa.PrivateKey
	gatePEM    string
	gateBase64 string
}

func newKeys(t *testing.T) *keys {
	t.Helper()

	app, err := rsa.GenerateKey(rand.Reader, _keyBits)
	require.NoError(t, err)

	gate, err := rsa.GenerateKey(rand.Reader, _keyBits)
	require.NoError(t, err)

	appDER, err := x509.MarshalPKCS8PrivateKey(app)
	require.NoError(t, err)

	pubDER, err := x509.MarshalPKIXPublicKey(&gate.PublicKey)
	require.NoError(t, err)

	return &keys{
		app: app, appBase64: base64.StdEncoding.EncodeToString(appDER), gate: gate,
		gatePEM:    string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})),
		gateBase64: base64.StdEncoding.EncodeToString(pubDER),
	}
}

func sign(t *testing.T, key *rsa.PrivateKey, data string) string {
	t.Helper()

	sum := sha256.Sum256([]byte(data))
	sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, sum[:])
	require.NoError(t, err)

	return base64.StdEncoding.EncodeToString(sig)
}

func client(t *testing.T, k *keys, h http.HandlerFunc) *alipay.Client {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	c, err := alipay.New(&alipay.Config{AppID: "2021", PrivateKey: k.appBase64, PublicKey: k.gatePEM, Gateway: srv.URL + "/gateway.do"}, nil)
	require.NoError(t, err)

	return c
}

// signedReply writes a token response whose node is signed by the gateway key.
func signedReply(t *testing.T, key *rsa.PrivateKey, node string) http.HandlerFunc {
	t.Helper()

	signature := sign(t, key, node)

	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"alipay_system_oauth_token_response":` + node + `,"sign":"` + signature + `"}`))
	}
}

func TestHelpers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "a=1&b=2", alipay.SignString(map[string]string{"b": "2", "a": "1", "c": ""}))
	assert.Equal(t, "2026-01-01 08:00:00", alipay.BeijingTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
}

func TestNewRejectsBadKeys(t *testing.T) {
	t.Parallel()

	k := newKeys(t)

	_, err := alipay.New(&alipay.Config{AppID: "a", PrivateKey: "nope", PublicKey: k.gateBase64, Gateway: ""}, nil)
	require.ErrorContains(t, err, "ALIPAY_PRIVATE_KEY")

	_, err = alipay.New(&alipay.Config{AppID: "a", PrivateKey: k.appBase64, PublicKey: "nope", Gateway: ""}, nil)
	require.ErrorContains(t, err, "ALIPAY_PUBLIC_KEY")

	_, err = alipay.New(&alipay.Config{AppID: "a", PrivateKey: k.appBase64, PublicKey: k.gateBase64, Gateway: ""}, nil)
	require.NoError(t, err)
}

func TestExchangeSignsTheRequestAndVerifiesTheResponse(t *testing.T) {
	t.Parallel()

	k := newKeys(t)
	node := `{"user_id":"2088","open_id":"o1","access_token":"t","expires_in":3600}`
	reply := signedReply(t, k.gate, node)

	c := client(t, k, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "alipay.system.oauth.token", r.PostForm.Get("method"))
		assert.Equal(t, "RSA2", r.PostForm.Get("sign_type"))
		assert.Equal(t, "ac", r.PostForm.Get("code"))

		params := map[string]string{}

		for key := range r.PostForm {
			if key != "sign" {
				params[key] = r.PostForm.Get(key)
			}
		}

		sig, err := base64.StdEncoding.DecodeString(r.PostForm.Get("sign"))
		require.NoError(t, err)

		sum := sha256.Sum256([]byte(alipay.SignString(params)))
		require.NoError(t, rsa.VerifyPKCS1v15(&k.app.PublicKey, crypto.SHA256, sum[:], sig))

		reply(w, r)
	})

	id, err := c.Exchange(t.Context(), "ac")
	require.NoError(t, err)
	assert.Equal(t, &entity.ExternalIdentity{Provider: "alipay", Subject: "o1", UnionID: ""}, id)
}

func TestExchangeFallsBackToUserID(t *testing.T) {
	t.Parallel()

	k := newKeys(t)

	id, err := client(t, k, signedReply(t, k.gate, `{"user_id":"2088"}`)).Exchange(t.Context(), "ac")
	require.NoError(t, err)
	assert.Equal(t, "2088", id.Subject)
}

func TestExchangeErrors(t *testing.T) {
	t.Parallel()

	k := newKeys(t)
	other := newKeys(t)

	cases := []struct {
		name  string
		reply http.HandlerFunc
		key   string
	}{
		{"invalid code", body(`{"error_response":{"code":"40002","sub_code":"isv.code-invalid","msg":"bad"}}`), entity.KeyProviderCodeInvalid},
		{"other error", body(`{"error_response":{"code":"40004","sub_code":"isv.invalid-app-id"}}`), entity.KeyBadGateway},
		{"wrong signature", signedReply(t, other.gate, `{"user_id":"2088"}`), entity.KeyBadGateway},
		{"unsigned", body(`{"alipay_system_oauth_token_response":{"user_id":"2088"}}`), entity.KeyBadGateway},
		{"no subject", signedReply(t, k.gate, `{"access_token":"t"}`), entity.KeyBadGateway},
		{"unknown body", body(`{}`), entity.KeyBadGateway},
		{"not json", body(`<html>`), entity.KeyBadGateway},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := client(t, k, tc.reply).Exchange(t.Context(), "ac")
			require.ErrorIs(t, err, entity.New(tc.key))
		})
	}
}

func TestExchangeUnreachableIsBadGateway(t *testing.T) {
	t.Parallel()

	k := newKeys(t)

	c, err := alipay.New(&alipay.Config{AppID: "a", PrivateKey: k.appBase64, PublicKey: k.gateBase64, Gateway: "http://127.0.0.1:1"}, nil)
	require.NoError(t, err)

	_, err = c.Exchange(t.Context(), "ac")
	require.ErrorIs(t, err, entity.New(entity.KeyBadGateway))
}

func body(s string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(s))
	}
}
