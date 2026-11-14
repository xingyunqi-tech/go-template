//go:build integration

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

const _bucket = "files-test"

var _pngBytes = []byte("\x89PNG\r\n\x1a\nabc") //nolint:gochecknoglobals // fixture

var (
	_minioOnce sync.Once       //nolint:gochecknoglobals // one container per package
	_minio     *testutil.Minio //nolint:gochecknoglobals // one container per package
	_minioErr  error           //nolint:gochecknoglobals // one container per package
)

func minio(t *testing.T) *testutil.Minio {
	t.Helper()

	_minioOnce.Do(func() { _minio, _minioErr = testutil.StartMinio(context.Background(), _bucket) })
	require.NoError(t, _minioErr, "start minio")

	return _minio
}

func localFiles(t *testing.T) (mutate func(*config.Config), root string) {
	t.Helper()

	root = t.TempDir()

	return func(c *config.Config) {
		c.File.Driver = config.FileDriverLocal
		c.File.LocalRoot = root
		c.App.BackendDomain = "http://backend.test"
	}, root
}

func s3Files(t *testing.T, driver string) func(*config.Config) {
	t.Helper()

	m := minio(t)

	return func(c *config.Config) {
		c.File.Driver = driver
		c.File.AccessKeyID = m.AccessKey
		c.File.SecretAccessKey = config.Secret(m.SecretKey)
		c.File.Bucket = _bucket
		c.File.Region = "us-east-1"
		c.File.Endpoint = m.Endpoint
		c.App.BackendDomain = "http://backend.test"
	}
}

func (a *testApp) upload(token, query, name string, content []byte) *result {
	a.t.Helper()

	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	h.Set("Content-Type", "application/octet-stream")

	pw, err := w.CreatePart(h)
	require.NoError(a.t, err)

	_, err = pw.Write(content)
	require.NoError(a.t, err)
	require.NoError(a.t, w.Close())

	req, err := http.NewRequestWithContext(a.t.Context(), http.MethodPost, a.url+"/api/v1/files/upload"+query, &buf)
	require.NoError(a.t, err)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(a.t, err)

	out := &result{Status: res.StatusCode, Header: res.Header, Raw: string(raw), Body: map[string]any{}}
	require.NoError(a.t, json.Unmarshal(raw, &out.Body))

	return out
}

func fileOf(t *testing.T, r *result) map[string]any {
	t.Helper()

	f, ok := r.data()["file"].(map[string]any)
	require.True(t, ok, r.Raw)

	return f
}

func asString(t *testing.T, v any) string {
	t.Helper()

	s, ok := v.(string)
	require.True(t, ok, "%v", v)

	return s
}

func entries(t *testing.T, dir string) []string {
	t.Helper()

	list, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}

	require.NoError(t, err)

	names := make([]string, len(list))
	for i, e := range list {
		names[i] = e.Name()
	}

	return names
}

type fetched struct {
	StatusCode int
	Header     http.Header
}

func get(t *testing.T, url, token string, headers ...string) (res *fetched, body []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, err := client.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	return &fetched{StatusCode: resp.StatusCode, Header: resp.Header}, body
}

func TestLocalUploadDownloadAndPrivateAccess(t *testing.T) {
	t.Parallel()

	mutate, root := localFiles(t)
	a := newAppWith(t, 0, mutate, nil)
	john := a.login("john.doe@example.com", _secret00)
	admin := a.login("admin@example.com", _secret00)

	public := a.upload(john.access, "", "Photo.JPG", _pngBytes)
	require.Equal(t, http.StatusCreated, public.Status, public.Raw)

	f := fileOf(t, public)
	assert.Equal(t, "PUBLIC", f["visibility"])
	assert.Equal(t, "image/jpeg", f["mimeType"])
	assert.InDelta(t, len(_pngBytes), f["size"], 0)
	assert.Regexp(t, `^http://backend\.test/api/v1/files/[0-9a-f]{21}\.jpg$`, f["path"])

	name := filepath.Base(asString(t, f["path"]))
	stored, err := os.ReadFile(filepath.Join(root, name))
	require.NoError(t, err)
	assert.Equal(t, _pngBytes, stored)

	res, body := get(t, a.url+"/api/v1/files/"+name, "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, _pngBytes, body)
	assert.Equal(t, "image/jpeg", res.Header.Get("Content-Type"))
	assert.Equal(t, "public, max-age=0", res.Header.Get("Cache-Control"))
	assert.True(t, strings.HasPrefix(res.Header.Get("ETag"), `W/"b-`))

	notModified, _ := get(t, a.url+"/api/v1/files/"+name, "", "If-None-Match", res.Header.Get("ETag"))
	assert.Equal(t, http.StatusNotModified, notModified.StatusCode)

	partial, chunk := get(t, a.url+"/api/v1/files/"+name, "", "Range", "bytes=0-3")
	assert.Equal(t, http.StatusPartialContent, partial.StatusCode)
	assert.Equal(t, _pngBytes[:4], chunk)

	private := a.upload(john.access, "?purpose=private-image", "a.png", _pngBytes)
	require.Equal(t, http.StatusCreated, private.Status, private.Raw)

	pf := fileOf(t, private)
	assert.Equal(t, "http://backend.test/api/v1/files/private/"+asString(t, pf["id"]), pf["path"])

	res, body = get(t, a.url+"/api/v1/files/private/"+asString(t, pf["id"]), john.access)
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, _pngBytes, body)

	res, _ = get(t, a.url+"/api/v1/files/private/"+asString(t, pf["id"]), admin.access)
	assert.Equal(t, http.StatusForbidden, res.StatusCode)

	res, _ = get(t, a.url+"/api/v1/files/private/"+asString(t, f["id"]), admin.access)
	assert.Equal(t, http.StatusOK, res.StatusCode, "public files are readable by any user")

	var stem string
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "path" FROM "file" WHERE "id" = $1`, pf["id"]).Scan(&stem))
	res, _ = get(t, a.url+"/api/v1/files/"+filepath.Base(stem), "")
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestRejectedUploadsLeaveNoFile(t *testing.T) {
	t.Parallel()

	mutate, root := localFiles(t)
	a := newAppWith(t, 0, mutate, nil)
	john := a.login("john.doe@example.com", _secret00)

	tooLarge := a.upload(john.access, "", "big.png", make([]byte, 5*1024*1024+1))
	assert.Equal(t, http.StatusRequestEntityTooLarge, tooLarge.Status)
	assert.JSONEq(t, `{"code":413,"message":"PAYLOAD_TOO_LARGE","data":{"file":"File is too large"}}`, tooLarge.Raw)

	badQuery := a.upload(john.access, "?purpose=", "a.png", _pngBytes)
	assert.Equal(t, http.StatusUnprocessableEntity, badQuery.Status, badQuery.Raw)

	forbidden := a.upload(john.access, "?purpose=audio", "a.mp3", _pngBytes)
	assert.Equal(t, http.StatusForbidden, forbidden.Status)

	assert.Empty(t, entries(t, root))

	var n int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT count(*) FROM "file"`).Scan(&n))
	assert.Zero(t, n)
}

func TestS3UploadAndSignedReads(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, s3Files(t, config.FileDriverS3), nil)
	john := a.login("john.doe@example.com", _secret00)

	r := a.upload(john.access, "?purpose=private-image", "a.png", _pngBytes)
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)

	f := fileOf(t, r)
	signed := asString(t, f["path"])
	assert.Contains(t, signed, "X-Amz-Signature=")
	assert.Contains(t, signed, "X-Amz-Expires=3600")

	res, body := get(t, signed, "")
	require.Equal(t, http.StatusOK, res.StatusCode, string(body))
	assert.Equal(t, _pngBytes, body)
	assert.Equal(t, "image/png", res.Header.Get("Content-Type"))

	res, body = get(t, a.url+"/api/v1/files/private/"+asString(t, f["id"]), john.access)
	assert.Equal(t, http.StatusFound, res.StatusCode)
	assert.Contains(t, res.Header.Get("Location"), "X-Amz-Signature=")
	assert.True(t, strings.HasPrefix(string(body), "Found. Redirecting to "))

	res, _ = get(t, a.url+"/api/v1/files/x.png", "")
	assert.Equal(t, http.StatusNotFound, res.StatusCode)

	tooLarge := a.upload(john.access, "", "big.png", make([]byte, 5*1024*1024+1))
	assert.Equal(t, http.StatusRequestEntityTooLarge, tooLarge.Status)
}

func TestS3PresignedUpload(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, s3Files(t, config.FileDriverS3Presigned), nil)
	john := a.login("john.doe@example.com", _secret00)

	r := a.do(http.MethodPost, "/api/v1/files/upload", john.access, map[string]any{"fileName": "a.png", "fileSize": len(_pngBytes)})
	require.Equal(t, http.StatusCreated, r.Status, r.Raw)

	f := fileOf(t, r)
	signed, _ := r.data()["uploadSignedUrl"].(string)
	require.NotEmpty(t, signed)
	assert.InDelta(t, len(_pngBytes), f["size"], 0)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, signed, bytes.NewReader(_pngBytes))
	require.NoError(t, err)

	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)

	_, body := get(t, asString(t, f["path"]), "")
	assert.Equal(t, _pngBytes, body)

	assert.Equal(t, http.StatusRequestEntityTooLarge,
		a.do(http.MethodPost, "/api/v1/files/upload", john.access, map[string]any{"fileName": "a.png", "fileSize": 5*1024*1024 + 1}).Status)
	assert.Equal(t, http.StatusUnprocessableEntity,
		a.do(http.MethodPost, "/api/v1/files/upload", john.access, map[string]any{"fileName": "a.txt", "fileSize": 1}).Status)
	assert.Equal(t, http.StatusUnprocessableEntity,
		a.do(http.MethodPost, "/api/v1/files/upload", john.access, map[string]any{"fileName": 1}).Status)
}

func TestFilesCleanerDeletesUploadsButKeepsReferencedOnes(t *testing.T) {
	t.Parallel()

	mutate, root := localFiles(t)
	a := newAppWith(t, 0, mutate, nil)
	john := a.login("john.doe@example.com", _secret00)

	kept := fileOf(t, a.upload(john.access, "", "kept.png", _pngBytes))
	removed := fileOf(t, a.upload(john.access, "", "gone.png", _pngBytes))

	_, err := a.pool.Exec(t.Context(), `UPDATE "user" SET "photoId" = $1 WHERE "email" = 'admin@example.com'`, kept["id"])
	require.NoError(t, err)

	r := a.do(http.MethodDelete, "/api/v1/auth/me", john.access, map[string]any{"oldPassword": _secret00})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	startWorker(t, a, &captured{})

	require.Eventually(t, func() bool {
		var n int

		_ = a.pool.QueryRow(t.Context(), `SELECT count(*) FROM "account_cleanup"`).Scan(&n)

		return n == 1
	}, _waitFor, 50*time.Millisecond)

	var ids []string

	rows, err := a.pool.Query(t.Context(), `SELECT "id"::text FROM "file"`)
	require.NoError(t, err)

	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}

	assert.Equal(t, []string{asString(t, kept["id"])}, ids)
	assert.Equal(t, []string{filepath.Base(asString(t, kept["path"]))}, entries(t, root))
	assert.NotContains(t, entries(t, root), filepath.Base(asString(t, removed["path"])))
}
