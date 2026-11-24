//go:build e2e

package e2e_test

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/jwtx"
)

const (
	_mailWait = 30 * time.Second
	_password = "Passw0rd!"
)

var _tokenLink = regexp.MustCompile(`[?&]token=([A-Za-z0-9_-]+)`)

func mailpitURL() string {
	if u := os.Getenv("MAILPIT_URL"); u != "" {
		return u
	}

	return "http://localhost:18025"
}

func getJSON(t *testing.T, rawURL string, out any) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, http.NoBody)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, res.StatusCode, string(raw))
	require.NoError(t, json.Unmarshal(raw, out))
}

// mailToken waits for the mail Mailpit received for to and returns the token of its link.
func mailToken(t *testing.T, to string) string {
	t.Helper()

	deadline := time.Now().Add(_mailWait)

	for time.Now().Before(deadline) {
		var found struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}

		getJSON(t, mailpitURL()+"/api/v1/search?query="+url.QueryEscape("to:"+to), &found)

		if len(found.Messages) > 0 {
			var msg struct {
				Text string `json:"Text"`
			}

			getJSON(t, mailpitURL()+"/api/v1/message/"+found.Messages[0].ID, &msg)

			match := _tokenLink.FindStringSubmatch(msg.Text)
			require.NotNil(t, match, msg.Text)

			return match[1]
		}

		time.Sleep(_pollInterval)
	}

	t.Fatalf("no mail to %s", to)

	return ""
}

func loginData(t *testing.T, r *response) (access, refresh string) {
	t.Helper()

	data, ok := r.body["data"].(map[string]any)
	require.True(t, ok, string(r.raw))

	access, _ = data["accessToken"].(string)
	refresh, _ = data["refreshToken"].(string)

	return access, refresh
}

func TestRegisterVerifySignInRefreshAndSignOut(t *testing.T) {
	t.Parallel()

	email := fmt.Sprintf("e2e-%s@example.com", uuid.NewV7())

	registered := call(t, http.MethodPost, "/api/v1/auth/email/register", "", `{"email":"`+email+`","password":"`+_password+`"}`)
	require.Equal(t, http.StatusCreated, registered.status, string(registered.raw))

	verified := call(t, http.MethodPost, "/api/v1/auth/email/verify", "", `{"token":"`+mailToken(t, email)+`"}`)
	require.Equal(t, http.StatusOK, verified.status, string(verified.raw))

	login := call(t, http.MethodPost, "/api/v1/auth/email/login", "", `{"email":"`+email+`","password":"`+_password+`"}`)
	require.Equal(t, http.StatusOK, login.status, string(login.raw))

	access, refresh := loginData(t, login)

	me := call(t, http.MethodGet, "/api/v1/me", access, "")
	require.Equal(t, http.StatusOK, me.status)

	user, _ := me.body["data"].(map[string]any)
	assert.Equal(t, true, user["emailVerified"])

	rotated := call(t, http.MethodPost, "/api/v1/auth/refresh", "", `{"refreshToken":"`+refresh+`"}`)
	require.Equal(t, http.StatusOK, rotated.status, string(rotated.raw))

	access, _ = loginData(t, rotated)

	sessions := call(t, http.MethodGet, "/api/v1/me/sessions", access, "")
	require.Equal(t, http.StatusOK, sessions.status)

	list, _ := sessions.body["data"].([]any)
	assert.Len(t, list, 2)

	assert.Equal(t, http.StatusOK, call(t, http.MethodPost, "/api/v1/auth/logout", access, "").status)
	assert.Equal(t, http.StatusUnauthorized, call(t, http.MethodGet, "/api/v1/me", access, "").status)
}

func TestJWKSVerifiesTheIssuedTokens(t *testing.T) {
	t.Parallel()

	jwks := call(t, http.MethodGet, "/.well-known/jwks.json", "", "")
	require.Equal(t, http.StatusOK, jwks.status)
	assert.Equal(t, "max-age=300", jwks.header.Get("Cache-Control"))

	keys, _ := jwks.body["keys"].([]any)
	require.Len(t, keys, 2, "the signing key and AUTH_VERIFY_KEYS")

	var kids []any

	for _, k := range keys {
		key, _ := k.(map[string]any)
		kids = append(kids, key["kid"])
	}

	for _, priv := range []ed25519.PrivateKey{signingKey(), oldKey()} {
		pub, ok := priv.Public().(ed25519.PublicKey)
		require.True(t, ok)

		kid, err := jwtx.KeyID(pub)
		require.NoError(t, err)
		assert.Contains(t, kids, kid)
	}
}

func TestDeleteMeSignsOutAndFreesTheEmail(t *testing.T) {
	t.Parallel()

	email := fmt.Sprintf("e2e-%s@example.com", uuid.NewV7())

	registered := call(t, http.MethodPost, "/api/v1/auth/email/register", "", `{"email":"`+email+`","password":"`+_password+`"}`)
	require.Equal(t, http.StatusCreated, registered.status, string(registered.raw))

	access, refresh := loginData(t, registered)

	deleted := call(t, http.MethodDelete, "/api/v1/me", access, "")
	require.Equal(t, http.StatusAccepted, deleted.status, string(deleted.raw))

	assert.Equal(t, http.StatusUnauthorized, call(t, http.MethodGet, "/api/v1/me", access, "").status)
	assert.Equal(t, http.StatusUnauthorized, call(t, http.MethodPost, "/api/v1/auth/refresh", "", `{"refreshToken":"`+refresh+`"}`).status)

	again := call(t, http.MethodPost, "/api/v1/auth/email/register", "", `{"email":"`+email+`","password":"`+_password+`"}`)
	require.Equal(t, http.StatusCreated, again.status, string(again.raw))
}

func TestNoProviderIsEnabledWithoutCredentials(t *testing.T) {
	t.Parallel()

	providers := call(t, http.MethodGet, "/api/v1/auth/providers", "", "")
	require.Equal(t, http.StatusOK, providers.status)
	assert.Equal(t, []any{}, providers.body["data"])
}
