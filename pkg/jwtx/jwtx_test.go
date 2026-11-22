package jwtx_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	return priv
}

func publicKey(t *testing.T, priv ed25519.PrivateKey) ed25519.PublicKey {
	t.Helper()

	pub, ok := priv.Public().(ed25519.PublicKey)
	require.True(t, ok)

	return pub
}

func claims(now time.Time, ttl time.Duration) *jwtx.Claims {
	iat, exp := jwtx.Lifetime(now, ttl)

	return &jwtx.Claims{
		Subject: uuid.NewV7(), SessionID: uuid.NewV7(), Roles: []string{"user"},
		EmailVerified: true, AuthTime: iat, IssuedAt: iat, Expires: exp, ID: "jti-1",
	}
}

func sign(t *testing.T, priv ed25519.PrivateKey, c *jwtx.Claims, issuer, audience string) string {
	t.Helper()

	s, err := jwtx.NewSigner(priv, issuer, audience)
	require.NoError(t, err)

	token, err := s.Sign(c)
	require.NoError(t, err)

	return token
}

func decodeSegment(t *testing.T, token string, i int) map[string]any {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[i])
	require.NoError(t, err)

	out := map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

func TestTokensCarryTheConventionClaims(t *testing.T) {
	t.Parallel()

	priv := newKey(t)
	c := claims(time.Now(), 15*time.Minute)
	token := sign(t, priv, c, "iss", "aud")

	pub := publicKey(t, priv)

	kid, err := jwtx.KeyID(pub)
	require.NoError(t, err)

	h := decodeSegment(t, token, 0)
	assert.Equal(t, "EdDSA", h["alg"])
	assert.Equal(t, kid, h["kid"])

	p := decodeSegment(t, token, 1)
	assert.Equal(t, "iss", p["iss"])
	assert.Equal(t, "aud", p["aud"])
	assert.Equal(t, c.Subject.String(), p["sub"])
	assert.Equal(t, c.SessionID.String(), p["sid"])
	assert.Equal(t, []any{"user"}, p["roles"])
	assert.Equal(t, true, p["email_verified"])
	assert.InDelta(t, float64(c.AuthTime.Unix()), p["auth_time"], 0)
	assert.Equal(t, "jti-1", p["jti"])

	keys, err := jwtx.NewStaticKeys(pub)
	require.NoError(t, err)

	got, err := jwtx.NewVerifier(keys, "iss", "aud").Verify(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, c.Subject, got.Subject)
	assert.Equal(t, c.SessionID, got.SessionID)
	assert.Equal(t, c.Roles, got.Roles)
	assert.True(t, got.EmailVerified)
	assert.Equal(t, c.AuthTime.Unix(), got.AuthTime.Unix())
}

func TestKeyIDIsTheRFC7638Thumbprint(t *testing.T) {
	t.Parallel()

	pub := ed25519.PublicKey(make([]byte, ed25519.PublicKeySize))
	x := base64.RawURLEncoding.EncodeToString(pub)

	kid, err := jwtx.KeyID(pub)
	require.NoError(t, err)

	key, err := jwk.ParseKey([]byte(`{"crv":"Ed25519","kty":"OKP","x":"` + x + `"}`))
	require.NoError(t, err)

	want, err := jwtx.PublicJWK(pub)
	require.NoError(t, err)

	gotKid, _ := want.KeyID()
	assert.Equal(t, kid, gotKid)

	alg, _ := want.Algorithm()
	assert.Equal(t, jwa.EdDSA().String(), alg.String())
	assert.Equal(t, jwa.OKP(), key.KeyType())
}

func TestVerifyRejects(t *testing.T) {
	t.Parallel()

	priv, other := newKey(t), newKey(t)
	keys, err := jwtx.NewStaticKeys(publicKey(t, priv))
	require.NoError(t, err)

	v := jwtx.NewVerifier(keys, "iss", "aud")
	now := time.Now()

	expired := claims(now.Add(-time.Hour), 15*time.Minute)
	_, err = v.Verify(t.Context(), sign(t, priv, expired, "iss", "aud"))
	require.ErrorIs(t, err, jwtx.ErrExpired)

	withinSkew := claims(now.Add(-15*time.Minute-10*time.Second), 15*time.Minute)
	_, err = v.Verify(t.Context(), sign(t, priv, withinSkew, "iss", "aud"))
	require.NoError(t, err)

	for name, token := range map[string]string{
		"wrong issuer":   sign(t, priv, claims(now, time.Minute), "other", "aud"),
		"wrong audience": sign(t, priv, claims(now, time.Minute), "iss", "other"),
		"unknown key":    sign(t, other, claims(now, time.Minute), "iss", "aud"),
		"garbage":        "a.b.c",
		"hs256": base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","kid":"x"}`)) + "." +
			base64.RawURLEncoding.EncodeToString([]byte(`{}`)) + ".sig",
	} {
		_, err := v.Verify(t.Context(), token)
		require.ErrorIs(t, err, jwtx.ErrInvalid, name)
	}
}

func signRaw(t *testing.T, priv ed25519.PrivateKey, payload map[string]any) string {
	t.Helper()

	kid, err := jwtx.KeyID(publicKey(t, priv))
	require.NoError(t, err)

	h, err := json.Marshal(map[string]any{"alg": "EdDSA", "kid": kid, "typ": "JWT"})
	require.NoError(t, err)

	p, err := json.Marshal(payload)
	require.NoError(t, err)

	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)

	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(input)))
}

func TestVerifyRejectsMissingClaimsAndAudienceArrays(t *testing.T) {
	t.Parallel()

	priv := newKey(t)
	keys, err := jwtx.NewStaticKeys(publicKey(t, priv))
	require.NoError(t, err)

	v := jwtx.NewVerifier(keys, "iss", "aud")
	now := time.Now().Unix()

	full := func() map[string]any {
		return map[string]any{
			"iss": "iss", "aud": "aud", "sub": uuid.NewV7().String(), "sid": uuid.NewV7().String(),
			"roles": []string{"user"}, "email_verified": true, "auth_time": now,
			"iat": now, "exp": now + 60, "jti": "jti-1",
		}
	}

	_, err = v.Verify(t.Context(), signRaw(t, priv, full()))
	require.NoError(t, err)

	for _, claim := range []string{"sub", "iat", "exp", "jti"} {
		payload := full()
		delete(payload, claim)

		_, err := v.Verify(t.Context(), signRaw(t, priv, payload))
		require.ErrorIs(t, err, jwtx.ErrInvalid, claim)
	}

	payload := full()
	payload["aud"] = []string{"aud"}

	_, err = v.Verify(t.Context(), signRaw(t, priv, payload))
	require.ErrorIs(t, err, jwtx.ErrInvalid)
}

func TestPEMRoundTrip(t *testing.T) {
	t.Parallel()

	priv := newKey(t)

	privPEM, err := jwtx.EncodePrivateKey(priv)
	require.NoError(t, err)

	parsed, err := jwtx.ParsePrivateKey(privPEM)
	require.NoError(t, err)
	assert.Equal(t, priv, parsed)

	a, err := jwtx.EncodePublicKey(publicKey(t, priv))
	require.NoError(t, err)

	b, err := jwtx.EncodePublicKey(publicKey(t, newKey(t)))
	require.NoError(t, err)

	pubs, err := jwtx.ParsePublicKeys(a + b)
	require.NoError(t, err)
	assert.Len(t, pubs, 2)

	_, err = jwtx.ParsePrivateKey("not pem")
	require.Error(t, err)
}
