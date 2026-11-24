//go:build e2e

package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _png = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64)...) //nolint:gochecknoglobals // fixed image header

func uploadAvatar(t *testing.T, access string) map[string]any {
	t.Helper()

	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("purpose", "avatar"))

	part, err := w.CreateFormFile("file", "me.png")
	require.NoError(t, err)

	_, err = part.Write(_png)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL()+"/api/v1/files", &buf)
	require.NoError(t, err)

	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+access)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, res.StatusCode, string(raw))

	var body struct {
		Data map[string]any `json:"data"`
	}

	require.NoError(t, json.Unmarshal(raw, &body))

	return body.Data
}

func TestUploadAnAvatarAndDownloadIt(t *testing.T) {
	t.Parallel()

	login := call(t, http.MethodPost, "/api/v1/auth/email/login", "", `{"email":"user@example.com","password":"`+_password+`"}`)
	require.Equal(t, http.StatusOK, login.status, string(login.raw))

	access, _ := loginData(t, login)
	file := uploadAvatar(t, access)
	id, _ := file["id"].(string)

	me := call(t, http.MethodPatch, "/api/v1/me", access, `{"avatarFileId":"`+id+`"}`)
	require.Equal(t, http.StatusOK, me.status, string(me.raw))

	user, _ := me.body["data"].(map[string]any)
	link, _ := user["avatarUrl"].(string)
	require.NotEmpty(t, link)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+link, http.NoBody)
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	assert.Equal(t, _png, body)
	assert.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
}
