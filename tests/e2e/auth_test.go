//go:build e2e

package e2e_test

import (
	"bytes"
	"crypto/md5" //nolint:gosec // clients send MD5 passwords
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // see import

	return hex.EncodeToString(sum[:])
}

func call(t *testing.T, method, path, token string, body any) (status int, data map[string]any) {
	t.Helper()

	var reader io.Reader = http.NoBody

	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, baseURL()+path, reader)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	out := map[string]any{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))

	data, _ = out["data"].(map[string]any)

	return resp.StatusCode, data
}

func TestRegisterLoginRefreshLogout(t *testing.T) {
	t.Parallel()

	email := "e2e." + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com"
	password := md5Hex("Secret00")

	status, _ := call(t, http.MethodPost, "/api/v1/auth/email/register", "", map[string]any{
		"email": email, "password": password, "firstName": "E", "lastName": "2E",
	})
	require.Equal(t, http.StatusOK, status)

	status, login := call(t, http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": email, "password": password})
	require.Equal(t, http.StatusOK, status)

	access, _ := login["token"].(string)
	refresh, _ := login["refreshToken"].(string)

	status, me := call(t, http.MethodGet, "/api/v1/auth/me", access, nil)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, email, me["email"])

	status, rotated := call(t, http.MethodPost, "/api/v1/auth/refresh", refresh, nil)
	require.Equal(t, http.StatusOK, status)

	next, _ := rotated["token"].(string)

	status, _ = call(t, http.MethodPost, "/api/v1/auth/logout", next, nil)
	require.Equal(t, http.StatusOK, status)

	status, _ = call(t, http.MethodGet, "/api/v1/auth/me", next, nil)
	require.Equal(t, http.StatusUnauthorized, status)
}

func TestSeededAdminListsUsers(t *testing.T) {
	t.Parallel()

	status, login := call(t, http.MethodPost, "/api/v1/auth/email/login", "", map[string]any{"email": "admin@example.com", "password": md5Hex("Secret00")})
	require.Equal(t, http.StatusOK, status)

	access, _ := login["token"].(string)

	status, page := call(t, http.MethodGet, "/api/v1/users?limit=1", access, nil)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, page, "hasNextPage")
}
