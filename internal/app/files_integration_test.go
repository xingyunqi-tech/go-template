//go:build integration

package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

var _pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 128)...) //nolint:gochecknoglobals // fixed image header

func localFiles(root string) func(*config.Config) {
	return func(c *config.Config) {
		c.Storage.Driver, c.Storage.LocalRoot = config.StorageLocal, root
	}
}

// filePart is a field of an upload; a part with a filename is the file.
type filePart struct {
	field, filename string
	data            []byte
}

func (a *testApp) upload(token string, parts ...filePart) *result {
	a.t.Helper()

	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)

	for _, p := range parts {
		var (
			dst io.Writer
			err error
		)

		if p.filename != "" {
			dst, err = w.CreateFormFile(p.field, p.filename)
		} else {
			dst, err = w.CreateFormField(p.field)
		}

		require.NoError(a.t, err)

		_, err = dst.Write(p.data)
		require.NoError(a.t, err)
	}

	require.NoError(a.t, w.Close())

	req, err := http.NewRequestWithContext(a.t.Context(), http.MethodPost, a.url+"/api/v1/files", &buf)
	require.NoError(a.t, err)

	req.Header.Set("Content-Type", w.FormDataContentType())

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(a.t, err)

	out := &result{Status: res.StatusCode, Header: res.Header, Raw: string(raw), Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)

	a.checkResponse(http.MethodPost, "/api/v1/files", "", out)

	return out
}

func avatar(data []byte) []filePart {
	return []filePart{{field: "purpose", data: []byte("avatar")}, {field: "file", filename: "me.png", data: data}}
}

// fetched is the answer to a plain GET.
type fetched struct {
	status int
	header http.Header
	body   []byte
}

func (a *testApp) fetch(rawURL string) fetched {
	a.t.Helper()

	target := rawURL
	if strings.HasPrefix(rawURL, "/") {
		target = a.url + rawURL
	}

	req, err := http.NewRequestWithContext(a.t.Context(), http.MethodGet, target, http.NoBody)
	require.NoError(a.t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(a.t, err)

	return fetched{status: res.StatusCode, header: res.Header, body: body}
}

func TestUploadGetAndDownloadALocalFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	a := newIdentityApp(t, false, localFiles(root))
	s := a.register("ada@example.com")

	r := a.upload(s.access, avatar(_pngBytes)...)
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)

	f := r.data()
	assert.Equal(t, "me.png", f["name"])
	assert.Equal(t, "image/png", f["contentType"])
	assert.Equal(t, "avatar", f["purpose"])
	assert.InDelta(t, len(_pngBytes), f["size"], 0)

	id, _ := f["id"].(string)
	link, _ := f["url"].(string)

	got := a.do(http.MethodGet, "/api/v1/files/"+id, s.access, "")
	require.Equal(t, http.StatusOK, got.Status, got.Raw)

	other := a.register("bob@example.com")
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/files/"+id, other.access, "").Status)

	res := a.fetch(link)
	require.Equal(t, http.StatusOK, res.status, string(res.body))
	assert.Equal(t, _pngBytes, res.body)
	assert.Equal(t, "image/png", res.header.Get("Content-Type"))
	assert.Equal(t, "nosniff", res.header.Get("X-Content-Type-Options"))
	assert.Empty(t, res.header.Get("Content-Disposition"))

	u, err := url.Parse(link)
	require.NoError(t, err)

	q := u.Query()
	q.Set("signature", strings.Repeat("0", 64))
	u.RawQuery = q.Encode()

	res = a.fetch(u.String())
	assert.Equal(t, http.StatusNotFound, res.status, string(res.body))

	res = a.fetch(u.Path)
	assert.Equal(t, http.StatusNotFound, res.status)

	q.Set("signature", strings.Repeat("f", 4096))
	u.RawQuery = q.Encode()

	res = a.fetch(u.String())
	assert.Equal(t, http.StatusNotFound, res.status)
}

func TestNonImagesDownloadAsAttachments(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false, localFiles(t.TempDir()))
	s := a.register("ada@example.com")

	r := a.upload(s.access, filePart{field: "file", filename: "报告.pdf", data: []byte("%PDF-1.7\n%test\n")}, filePart{field: "purpose", data: []byte("attachment")})
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)

	link, _ := r.data()["url"].(string)

	res := a.fetch(link)
	require.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, "application/pdf", res.header.Get("Content-Type"))
	assert.Contains(t, res.header.Get("Content-Disposition"), "attachment")
}

func TestUploadRejections(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	a := newIdentityApp(t, false, localFiles(root), func(c *config.Config) { c.HTTP.BodyLimit = 1 << 10 })
	s := a.register("ada@example.com")

	big := append(append([]byte{}, _pngBytes...), make([]byte, 5<<20)...)

	r := a.upload(s.access, avatar(big)...)
	require.Equal(t, http.StatusRequestEntityTooLarge, r.Status, r.Raw)
	assert.Equal(t, "file.tooLarge", r.Body["errorKey"])

	huge := append(append([]byte{}, _pngBytes...), make([]byte, 21<<20)...)

	r = a.upload(s.access, filePart{field: "file", filename: "x.png", data: huge}, filePart{field: "purpose", data: []byte("attachment")})
	require.Equal(t, http.StatusRequestEntityTooLarge, r.Status, r.Raw)
	assert.Equal(t, "file.tooLarge", r.Body["errorKey"])

	r = a.upload(s.access, filePart{field: "purpose", data: []byte("avatar")}, filePart{field: "file", filename: "x.pdf", data: []byte("%PDF-1.7\n")})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	assert.Equal(t, "file.typeNotAllowed", r.Body["errorKey"])

	r = a.upload(s.access, filePart{field: "purpose", data: []byte("banner")})
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	assert.Contains(t, r.Raw, `"field":"purpose","rule":"enum"`)

	r = a.do(http.MethodPost, "/api/v1/files", s.access, `{"purpose":"avatar"}`)
	require.Equal(t, http.StatusUnsupportedMediaType, r.Status, r.Raw)

	assert.Equal(t, http.StatusUnauthorized, a.upload("", avatar(_pngBytes)...).Status)

	r = a.upload(s.access, avatar(_pngBytes)...)
	require.Equal(t, http.StatusCreated, r.Status, "uploads are not held to BODY_LIMIT: %s", r.Raw)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "rejected uploads leave nothing behind")
}

func TestAvatarFileIDMustBeAnAvatarOfTheCaller(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false, localFiles(t.TempDir()))
	s := a.register("ada@example.com")

	r := a.upload(s.access, avatar(_pngBytes)...)
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)

	avatarID, _ := r.data()["id"].(string)

	r = a.upload(s.access, filePart{field: "purpose", data: []byte("attachment")}, filePart{field: "file", filename: "a.png", data: _pngBytes})
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)

	attachmentID, _ := r.data()["id"].(string)

	other := a.register("bob@example.com")

	for _, tc := range []struct {
		token, id string
	}{{s.access, attachmentID}, {other.access, avatarID}, {s.access, "0190a5e0-0000-7000-8000-000000000001"}} {
		r = a.do(http.MethodPatch, "/api/v1/me", tc.token, `{"avatarFileId":"`+tc.id+`"}`)
		require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
		assert.Contains(t, r.Raw, `"field":"avatarFileId","rule":"exists"`)
	}

	r = a.do(http.MethodPatch, "/api/v1/me", s.access, `{"avatarFileId":"`+avatarID+`"}`)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	link, _ := r.data()["avatarUrl"].(string)
	require.NotEmpty(t, link)

	res := a.fetch(link)
	require.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, _pngBytes, res.body)

	me := a.do(http.MethodGet, "/api/v1/me", s.access, "")
	assert.NotNil(t, me.data()["avatarUrl"])

	login := a.login("ada@example.com", _password)
	assert.NotNil(t, sessionOf(t, login).user["avatarUrl"])

	r = a.do(http.MethodPatch, "/api/v1/me", s.access, `{"displayName":"Ada L"}`)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.NotNil(t, r.data()["avatarUrl"])

	r = a.do(http.MethodPatch, "/api/v1/me", s.access, `{"avatarFileId":null}`)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)
	assert.Nil(t, r.data()["avatarUrl"])
}

func TestDeletedAccountsLoseTheirFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	a := newIdentityApp(t, false, localFiles(root), immediatePurge)
	s := a.register("ada@example.com")
	id, _ := s.user["id"].(string)

	require.Equal(t, http.StatusCreated, a.upload(s.access, avatar(_pngBytes)...).Status)
	require.Equal(t, http.StatusAccepted, a.do(http.MethodDelete, "/api/v1/me", s.access, "").Status)

	a.waitPurged(id)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Equal(t, 0, a.count(`SELECT count(*) FROM file WHERE owner_sub = $1`, id))
}

func TestUploadToS3PresignsTheURL(t *testing.T) {
	t.Parallel()

	m, err := testutil.StartMinio(t.Context(), "files")
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Terminate() })

	a := newIdentityApp(t, false, func(c *config.Config) {
		c.Storage.Driver, c.Storage.Bucket, c.Storage.Region, c.Storage.Endpoint = config.StorageS3, m.Bucket, "us-east-1", m.Endpoint
		c.Storage.AccessKeyID, c.Storage.SecretAccessKey, c.Storage.ForcePathStyle = m.AccessKey, config.Secret(m.SecretKey), true
		c.Storage.URLTTL = time.Minute
	}, immediatePurge)
	s := a.register("ada@example.com")
	id, _ := s.user["id"].(string)

	r := a.upload(s.access, filePart{field: "purpose", data: []byte("attachment")}, filePart{field: "file", filename: "a.pdf", data: []byte("%PDF-1.7\n%x\n")})
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)

	link, _ := r.data()["url"].(string)
	assert.True(t, strings.HasPrefix(link, m.Endpoint), link)
	assert.Contains(t, link, "X-Amz-Expires=60")

	res := a.fetch(link)
	require.Equal(t, http.StatusOK, res.status, string(res.body))
	assert.Equal(t, "%PDF-1.7\n%x\n", string(res.body))
	assert.Contains(t, res.header.Get("Content-Disposition"), "attachment")

	fileID, _ := r.data()["id"].(string)

	res = a.fetch("/api/v1/files/" + fileID + "/content?expires=1&signature=x")
	assert.Equal(t, http.StatusNotFound, res.status)

	require.Equal(t, http.StatusAccepted, a.do(http.MethodDelete, "/api/v1/me", s.access, "").Status)
	a.waitPurged(id)

	res = a.fetch(link)
	assert.Equal(t, http.StatusNotFound, res.status)
}
