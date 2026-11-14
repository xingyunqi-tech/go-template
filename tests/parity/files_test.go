//go:build parity

package parity_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const _upload = "/api/v1/files/upload"

var (
	_png        = []byte("\x89PNG\r\n\x1a\nabc")                                                     //nolint:gochecknoglobals // fixture
	_uuid       = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`) //nolint:gochecknoglobals // fixture
	_storedName = regexp.MustCompile(`/files/[^/]+\.(\w+)$`)
)

// normalizeFile hides file ids and stored names.
func normalizeFile(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = normalizeFile(item, k)
		}

		return out
	case string:
		if key == "id" || key == "path" {
			x = _uuid.ReplaceAllString(x, "<uuid>")
			if key == "path" && !strings.Contains(x, "/private/") {
				x = _storedName.ReplaceAllString(x, "/files/<name>.$1")
			}

			return x
		}
	}

	return normalize(v, key)
}

func requireSameFileBody(t *testing.T, p pair) {
	t.Helper()

	require.Equal(t, p.Nestjs.Status, p.Go.Status, "go: %s\nnestjs: %s", p.Go.Body, p.Nestjs.Body)
	require.Equal(t, p.Nestjs.Header.Get("Content-Type"), p.Go.Header.Get("Content-Type"))
	require.Equal(t, normalizeFile(p.Nestjs.JSON(t), ""), normalizeFile(p.Go.JSON(t), ""))
}

type part struct {
	field, filename, contentType string
	content                      []byte
	isFile                       bool
}

func filePart(field, name, contentType string, content []byte) part {
	return part{field: field, filename: name, contentType: contentType, content: content, isFile: true}
}

func textPart(field, value string) part {
	return part{field: field, filename: "", contentType: "", content: []byte(value), isFile: false}
}

// form encodes parts like httpx files=.
func form(parts ...part) option {
	var buf bytes.Buffer

	w := multipart.NewWriter(&buf)

	for _, p := range parts {
		h := textproto.MIMEHeader{}
		if p.isFile {
			h.Set("Content-Disposition", `form-data; name="`+p.field+`"; filename="`+p.filename+`"`)

			if p.contentType != "" {
				h.Set("Content-Type", p.contentType)
			}
		} else {
			h.Set("Content-Disposition", `form-data; name="`+p.field+`"`)
		}

		pw, err := w.CreatePart(h)
		if err != nil {
			panic(err)
		}

		_, _ = pw.Write(p.content)
	}

	_ = w.Close()

	return withBody(w.FormDataContentType(), buf.Bytes())
}

func rawMultipart(body string) option {
	return withBody("multipart/form-data; boundary=xyz", []byte(body))
}

func disposition(params string) string {
	return "--xyz\r\nContent-Disposition: form-data; " + params + "\r\n\r\nabc\r\n--xyz--\r\n"
}

func uploadTarget(query url.Values) string {
	if len(query) == 0 {
		return _upload
	}

	return _upload + "?" + query.Encode()
}

func TestUpload(t *testing.T) {
	t.Parallel()

	user := tempUser(t, "files")
	big := func(n int) []byte { return make([]byte, n) }

	cases := []struct {
		query url.Values
		body  func(t *testing.T) option
	}{
		{nil, func(*testing.T) option { return form(filePart("file", "a.png", "image/png", _png)) }},
		{url.Values{"purpose": {"private-image"}}, func(*testing.T) option { return form(filePart("file", "a.png", "image/png", _png)) }},
		{nil, func(*testing.T) option {
			return form(filePart("file", "Photo.tar.JPEG", "image/jpeg", _png))
		}},
		{nil, func(*testing.T) option {
			return form(filePart("file", "../../x.png", "image/png", _png))
		}},
		{nil, func(*testing.T) option { return form(filePart("file", "empty.png", "image/png", nil)) }},
		{url.Values{"purpose": {"xx"}}, func(*testing.T) option { return form(filePart("file", "a.png", "image/png", _png)) }},
		{url.Values{"purpose": {"image", "image"}}, func(*testing.T) option { return form(filePart("file", "a.png", "image/png", _png)) }},
		{url.Values{"purpose": {""}}, func(*testing.T) option { return form(textPart("x", "1")) }},
		{url.Values{"purpose": {"audio"}}, func(*testing.T) option { return form(filePart("file", "a.mp3", "audio/mpeg", _png)) }},
		{nil, func(*testing.T) option {
			return form(filePart("file", "a.txt", "text/plain", []byte("hi")))
		}},
		{nil, func(*testing.T) option { return form(filePart("file", "png", "image/png", _png)) }},
		{nil, func(*testing.T) option { return form(filePart("other", "a.png", "image/png", _png)) }},
		{nil, func(*testing.T) option {
			return form(filePart("file", "a.png", "application/octet-stream", _png), filePart("file", "b.png", "application/octet-stream", _png))
		}},
		{nil, func(*testing.T) option { return form(filePart("file", "", "image/png", _png)) }},
		{nil, func(*testing.T) option { return form(textPart("file", "text")) }},
		{nil, func(t *testing.T) option {
			t.Helper()

			return withJSON(t, map[string]any{"fileName": "a.png", "fileSize": 10})
		}},
		{nil, func(*testing.T) option { return func(*requestOptions) {} }},
		{nil, func(*testing.T) option { return withBody("application/x-www-form-urlencoded", []byte("file=abc")) }},
		{nil, func(*testing.T) option {
			return form(filePart("file", "big.png", "image/png", big(5*1024*1024+1)))
		}},
		{nil, func(*testing.T) option {
			return form(filePart("file", "big.png", "image/png", big(5*1024*1024+20000)))
		}},
		{nil, func(*testing.T) option { return rawMultipart("garbage") }},
		{nil, func(*testing.T) option { return rawMultipart("--xyz\r\nBad Header\r\n\r\nabc\r\n--xyz--\r\n") }},
		{nil, func(*testing.T) option {
			return rawMultipart("preamble\r\n" + disposition(`name="file"; filename="a.png"`))
		}},
		{nil, func(*testing.T) option { return rawMultipart(disposition(`filename="a.png"`)) }},
		{nil, func(*testing.T) option {
			d := disposition(`name="file"; filename="a.png"`)

			return rawMultipart(d[:len(d)-11])
		}},
		{nil, func(*testing.T) option { return withBody("multipart/form-data", []byte("x")) }},
		{nil, func(*testing.T) option { return withBody("multipart/mixed; boundary=xyz", []byte("x")) }},
	}

	for _, lang := range []string{"en", "zh-CN"} {
		for i, tc := range cases {
			t.Run(lang+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()

				requireSameFileBody(t, do(t, http.MethodPost, uploadTarget(tc.query),
					withTokens(user), withHeader("Accept-Language", lang), tc.body(t)))
			})
		}
	}
}

func TestUploadRequiresJWT(t *testing.T) {
	t.Parallel()

	requireSameFileBody(t, do(t, http.MethodPost, uploadTarget(url.Values{"purpose": {"xx"}}),
		form(filePart("file", "a.png", "image/png", _png))))
}

func TestAdminUploadsContentAsset(t *testing.T) {
	t.Parallel()

	admin := loginBoth(t, "admin@example.com")
	requireSameFileBody(t, do(t, http.MethodPost, uploadTarget(url.Values{"purpose": {"audio"}}),
		withTokens(admin), form(filePart("file", "a.m4a", "audio/mp4", _png))))
}

func uploadPair(t *testing.T, user [2]string, purpose string) [2]map[string]any {
	t.Helper()

	p := do(t, http.MethodPost, uploadTarget(url.Values{"purpose": {purpose}}), withTokens(user),
		form(filePart("file", "a.png", "image/png", _png)))

	var out [2]map[string]any

	for i, r := range []*response{p.Go, p.Nestjs} {
		require.Equal(t, http.StatusCreated, r.Status, string(r.Body))
		out[i], _ = dataOf(t, r)["file"].(map[string]any)
	}

	return out
}

func str(t *testing.T, m map[string]any, key string) string {
	t.Helper()

	v, ok := m[key].(string)
	require.True(t, ok, "%s: %v", key, m)

	return v
}

func lastSegment(s string) string {
	return s[strings.LastIndexByte(s, '/')+1:]
}

func TestPublicFileDownload(t *testing.T) {
	t.Parallel()

	files := uploadPair(t, tempUser(t, "download"), "image")
	p := do(t, http.MethodGet, "", withTargets([2]string{
		"/api/v1/files/" + lastSegment(str(t, files[0], "path")),
		"/api/v1/files/" + lastSegment(str(t, files[1], "path")),
	}))

	require.Equal(t, p.Nestjs.Status, p.Go.Status)
	require.Equal(t, p.Nestjs.Body, p.Go.Body)

	for _, h := range []string{"Content-Type", "Cache-Control", "Accept-Ranges", "Content-Length"} {
		require.Equal(t, p.Nestjs.Header.Get(h), p.Go.Header.Get(h), h)
	}

	require.True(t, strings.HasPrefix(p.Go.Header.Get("ETag"), `W/"b-`), p.Go.Header.Get("ETag"))
	require.True(t, strings.HasPrefix(p.Nestjs.Header.Get("ETag"), `W/"b-`), p.Nestjs.Header.Get("ETag"))
}

func TestDownloadErrors(t *testing.T) {
	t.Parallel()

	user := tempUser(t, "download-errors")

	for _, target := range []string{
		"/api/v1/files/nope.png",
		"/api/v1/files/upload",
		"/api/v1/files/private/abc",
		"/api/v1/files/private/00000000-0000-4000-8000-000000000000",
	} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			requireSameFileBody(t, do(t, http.MethodGet, target))
			requireSameFileBody(t, do(t, http.MethodGet, target, withTokens(user)))
		})
	}
}

func TestPrivateFileIDValidation(t *testing.T) {
	t.Parallel()

	user := tempUser(t, "private-id")

	for _, id := range []string{
		"abc",
		"00000000-0000-9000-0000-000000000000",
		"%7B00000000-0000-4000-8000-000000000000%7D",
		"00000000-0000-0000-0000-000000000000",
		"FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF",
	} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			requireSameFileBody(t, do(t, http.MethodGet, "/api/v1/files/private/"+id, withTokens(user)))
		})
	}
}

func TestPrivateFileAccess(t *testing.T) {
	t.Parallel()

	owner := tempUser(t, "private-owner")
	admin := loginBoth(t, "admin@example.com")
	files := uploadPair(t, owner, "private-image")
	targets := [2]string{
		"/api/v1/files/private/" + str(t, files[0], "id"),
		"/api/v1/files/private/" + str(t, files[1], "id"),
	}

	for _, viewer := range []struct {
		tokens [2]string
		status int
	}{{owner, http.StatusOK}, {admin, http.StatusForbidden}} {
		p := do(t, http.MethodGet, "", withTargets(targets), withTokens(viewer.tokens))

		require.Equal(t, viewer.status, p.Go.Status, string(p.Go.Body))
		require.Equal(t, viewer.status, p.Nestjs.Status, string(p.Nestjs.Body))
		require.Equal(t, p.Nestjs.Header.Get("Content-Type"), p.Go.Header.Get("Content-Type"))
		require.Equal(t, p.Nestjs.Body, p.Go.Body)
	}
}
