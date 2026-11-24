package file_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/storage"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
)

var _png = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...) //nolint:gochecknoglobals // fixed image header

type rows struct {
	mu    sync.Mutex
	files map[uuid.UUID]entity.File
	err   error
}

func (r *rows) Create(_ context.Context, f *entity.File) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.err != nil {
		return r.err
	}

	f.CreatedAt = time.Now()
	r.files[f.ID] = *f

	return nil
}

func (r *rows) Get(_ context.Context, id uuid.UUID) (*entity.File, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	f, ok := r.files[id]

	return &f, ok, nil
}

func (r *rows) ListByID(_ context.Context, ids []uuid.UUID) ([]entity.File, error) {
	var out []entity.File

	for _, id := range ids {
		if _, ok := r.files[id]; ok {
			out = append(out, r.files[id])
		}
	}

	return out, nil
}

func (r *rows) ListOwner(_ context.Context, owner uuid.UUID) ([]entity.File, error) {
	var out []entity.File

	for id := range r.files {
		if r.files[id].OwnerSub == owner {
			out = append(out, r.files[id])
		}
	}

	return out, nil
}

func (r *rows) DeleteOwner(_ context.Context, owner uuid.UUID) error {
	for id := range r.files {
		if r.files[id].OwnerSub == owner {
			delete(r.files, id)
		}
	}

	return nil
}

type fixture struct {
	svc  *file.Service
	rows *rows
	root string
	now  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{rows: &rows{files: map[uuid.UUID]entity.File{}}, root: t.TempDir(), now: time.Unix(1_800_000_000, 0)}
	f.svc = file.New(f.rows, storage.NewLocal(f.root), file.Config{Secret: []byte("secret"), TTL: time.Hour},
		slog.New(slog.DiscardHandler), func() time.Time { return f.now })

	return f
}

// part is one field of a multipart body; a file part has a name.
type part struct {
	field, filename string
	data            []byte
}

func form(t *testing.T, parts ...part) usecase.Form {
	t.Helper()

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

		require.NoError(t, err)

		_, err = dst.Write(p.data)
		require.NoError(t, err)
	}

	require.NoError(t, w.Close())

	return multipart.NewReader(&buf, w.Boundary())
}

func (f *fixture) objects(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(f.root)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

func TestUploadInEitherOrderDetectsTheType(t *testing.T) {
	t.Parallel()

	for name, parts := range map[string][]part{
		"purpose first": {{field: "purpose", data: []byte("avatar")}, {field: "file", filename: "../me.png", data: _png}},
		"file first":    {{field: "file", filename: "me.png", data: _png}, {field: "purpose", data: []byte("avatar")}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			owner := uuid.NewV7()

			v, err := f.svc.Upload(t.Context(), owner, form(t, parts...))
			require.NoError(t, err)
			assert.Equal(t, "me.png", v.File.Name)
			assert.Equal(t, entity.TypePNG, v.File.ContentType)
			assert.Equal(t, int64(len(_png)), v.File.Size)
			assert.Len(t, v.File.SHA256, 32)
			assert.Equal(t, "local", v.File.Storage)
			assert.Equal(t, []string{v.File.ObjectKey}, f.objects(t))
			assert.True(t, strings.HasPrefix(v.URL, "/api/v1/files/"+v.File.ID.String()+"/content?"))
		})
	}
}

func TestUploadRejections(t *testing.T) {
	t.Parallel()

	big := append(append([]byte{}, _png...), make([]byte, 5<<20)...)

	cases := []struct {
		name  string
		parts []part
		check func(t *testing.T, err error)
	}{
		{
			"pdf avatar",
			[]part{{field: "purpose", data: []byte("avatar")}, {field: "file", filename: "a.pdf", data: []byte("%PDF-1.7\n")}},
			func(t *testing.T, err error) {
				t.Helper()
				require.ErrorIs(t, err, entity.New(entity.KeyFileTypeNotAllowed))
			},
		},
		{
			"too large with purpose first",
			[]part{{field: "purpose", data: []byte("avatar")}, {field: "file", filename: "a.png", data: big}},
			func(t *testing.T, err error) { t.Helper(); require.ErrorIs(t, err, entity.New(entity.KeyFileTooLarge)) },
		},
		{
			"too large with file first",
			[]part{{field: "file", filename: "a.png", data: big}, {field: "purpose", data: []byte("avatar")}},
			func(t *testing.T, err error) { t.Helper(); require.ErrorIs(t, err, entity.New(entity.KeyFileTooLarge)) },
		},
		{
			"unknown purpose",
			[]part{{field: "purpose", data: []byte("banner")}},
			func(t *testing.T, err error) { t.Helper(); assert.Equal(t, entity.RuleEnum, fields(t, err)[0].Rule) },
		},
		{"nothing", nil, func(t *testing.T, err error) {
			t.Helper()
			assert.Equal(t, []string{"file", "purpose"}, names(fields(t, err)))
		}},
		{
			"two files",
			[]part{{field: "file", filename: "a.png", data: _png}, {field: "file", filename: "b.png", data: _png}},
			func(t *testing.T, err error) {
				t.Helper()
				assert.Equal(t, entity.RuleMaxItems, fields(t, err)[0].Rule)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			_, err := f.svc.Upload(t.Context(), uuid.NewV7(), form(t, tc.parts...))
			tc.check(t, err)
			assert.Empty(t, f.objects(t), "rejected uploads leave nothing behind")
		})
	}
}

func TestUploadRemovesTheObjectWhenTheRowFails(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.rows.err = errors.New("db down")

	_, err := f.svc.Upload(t.Context(), uuid.NewV7(), form(t, part{field: "purpose", data: []byte("avatar")}, part{field: "file", filename: "a.png", data: _png}))
	require.ErrorIs(t, err, f.rows.err)
	assert.Empty(t, f.objects(t))
}

func TestUploadOfAnOversizedBodyIsTooLarge(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	_, err := f.svc.Upload(t.Context(), uuid.NewV7(), failingForm{err: entity.New(entity.KeyFileTooLarge)})
	require.ErrorIs(t, err, entity.New(entity.KeyFileTooLarge))

	_, err = f.svc.Upload(t.Context(), uuid.NewV7(), failingForm{err: errors.New("malformed")})
	require.ErrorIs(t, err, entity.New(entity.KeyBadRequest))
}

type failingForm struct{ err error }

func (f failingForm) NextPart() (*multipart.Part, error) { return nil, f.err }

func fields(t *testing.T, err error) []entity.FieldError {
	t.Helper()

	var verr *entity.ValidationError

	require.ErrorAs(t, err, &verr)

	return verr.Errors
}

func names(fs []entity.FieldError) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Field)
	}

	return out
}

func (f *fixture) upload(t *testing.T, owner uuid.UUID, purpose, name string, data []byte) *usecase.FileView {
	t.Helper()

	v, err := f.svc.Upload(t.Context(), owner, form(t, part{field: "purpose", data: []byte(purpose)}, part{field: "file", filename: name, data: data}))
	require.NoError(t, err)

	return v
}

func signedQuery(t *testing.T, raw string) (expires, signature string) {
	t.Helper()

	u, err := url.Parse(raw)
	require.NoError(t, err)

	return u.Query().Get("expires"), u.Query().Get("signature")
}

func TestContentNeedsAValidUnexpiredSignature(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	v := f.upload(t, uuid.NewV7(), "avatar", "a.png", _png)
	expires, signature := signedQuery(t, v.URL)

	got, body, err := f.svc.Content(t.Context(), v.File.ID, expires, signature)
	require.NoError(t, err)

	data, _ := io.ReadAll(body)
	_ = body.Close()

	assert.Equal(t, _png, data)
	assert.Equal(t, v.File.ID, got.ID)

	for name, args := range map[string][2]string{
		"wrong signature": {expires, strings.Repeat("0", len(signature))},
		"other expiry":    {"1900000000", signature},
		"no expiry":       {"", signature},
		"no signature":    {expires, ""},
	} {
		_, _, err := f.svc.Content(t.Context(), v.File.ID, args[0], args[1])
		require.ErrorIs(t, err, entity.New(entity.KeyNotFound), name)
	}

	_, _, err = f.svc.Content(t.Context(), uuid.NewV7(), expires, signature)
	require.ErrorIs(t, err, entity.New(entity.KeyNotFound))

	f.now = f.now.Add(2 * time.Hour)

	_, _, err = f.svc.Content(t.Context(), v.File.ID, expires, signature)
	require.ErrorIs(t, err, entity.New(entity.KeyNotFound))
}

func TestContentOfAMissingObjectIsNotFound(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	v := f.upload(t, uuid.NewV7(), "avatar", "a.png", _png)
	require.NoError(t, os.Remove(filepath.Join(f.root, v.File.ObjectKey)))

	expires, signature := signedQuery(t, v.URL)

	_, _, err := f.svc.Content(t.Context(), v.File.ID, expires, signature)
	require.ErrorIs(t, err, entity.New(entity.KeyNotFound))
}

func TestGetShowsOnlyTheOwnersFiles(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	owner := uuid.NewV7()
	v := f.upload(t, owner, "attachment", "doc.pdf", []byte("%PDF-1.7\n"))

	got, err := f.svc.Get(t.Context(), owner, v.File.ID)
	require.NoError(t, err)
	assert.Equal(t, entity.TypePDF, got.File.ContentType)

	_, err = f.svc.Get(t.Context(), uuid.NewV7(), v.File.ID)
	require.ErrorIs(t, err, entity.New(entity.KeyNotFound))
}

func TestAvatarsAndURLs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	owner := uuid.NewV7()
	avatar := f.upload(t, owner, "avatar", "a.png", _png)
	doc := f.upload(t, owner, "attachment", "a.png", _png)

	for name, want := range map[string]bool{"own avatar": true, "attachment": false, "other owner": false} {
		id, who := avatar.File.ID, owner

		switch name {
		case "attachment":
			id = doc.File.ID
		case "other owner":
			who = uuid.NewV7()
		}

		ok, err := f.svc.IsAvatar(t.Context(), who, id)
		require.NoError(t, err)
		assert.Equal(t, want, ok, name)
	}

	urls, err := f.svc.URLs(t.Context(), []uuid.UUID{avatar.File.ID, uuid.NewV7()})
	require.NoError(t, err)
	assert.Len(t, urls, 1)
	assert.Contains(t, urls[avatar.File.ID], avatar.File.ID.String())

	empty, err := f.svc.URLs(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestPurgeRemovesTheObjectsAndRowsOfTheOwner(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	owner, other := uuid.NewV7(), uuid.NewV7()
	f.upload(t, owner, "avatar", "a.png", _png)
	kept := f.upload(t, other, "avatar", "b.png", _png)

	require.NoError(t, f.svc.Purge(t.Context(), owner))
	assert.Equal(t, []string{kept.File.ObjectKey}, f.objects(t))
	assert.Len(t, f.rows.files, 1)
}

func TestLocalStorageRejectsPathKeys(t *testing.T) {
	t.Parallel()

	s := storage.NewLocal(t.TempDir())

	for _, key := range []string{"", "..", "a/b", "../x"} {
		_, err := s.NewSink(key)
		require.Error(t, err, key)

		_, err = s.Open(t.Context(), key)
		require.Error(t, err, key)
		require.Error(t, s.Delete(t.Context(), key), key)
	}

	_, err := s.Open(t.Context(), "missing")
	require.ErrorIs(t, err, repo.ErrObjectNotFound)
	require.NoError(t, s.Delete(t.Context(), "missing"))

	_, err = s.PresignGet(t.Context(), "k", "n", false, time.Minute)
	require.Error(t, err)
}

func TestUploadKeepsAPrintableUTF8Name(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	v := f.upload(t, uuid.NewV7(), "avatar", "a\xffb\tc.png", _png)
	assert.Equal(t, "a\uFFFDbc.png", v.File.Name)

	long := f.upload(t, uuid.NewV7(), "avatar", strings.Repeat("名", 300)+".png", _png)
	assert.Equal(t, 255, utf8.RuneCountInString(long.File.Name))
}

type brokenStorage struct {
	repo.FileStorage
}

func (brokenStorage) NewSink(string) (repo.Sink, error) { return brokenSink{}, nil }

type brokenSink struct{}

func (brokenSink) Write([]byte) (int, error)            { return 0, errors.New("disk full") }
func (brokenSink) Commit(context.Context, string) error { return nil }
func (brokenSink) Discard(context.Context)              {}

func TestStorageWriteFailureIsNotTheClientsFault(t *testing.T) {
	t.Parallel()

	svc := file.New(&rows{files: map[uuid.UUID]entity.File{}}, brokenStorage{storage.NewLocal(t.TempDir())},
		file.Config{Secret: []byte("s"), TTL: time.Hour}, slog.New(slog.DiscardHandler), nil)

	_, err := svc.Upload(t.Context(), uuid.NewV7(), form(t, part{field: "purpose", data: []byte("avatar")}, part{field: "file", filename: "a.png", data: _png}))
	require.ErrorContains(t, err, "disk full")
	require.NotErrorIs(t, err, entity.New(entity.KeyBadRequest))
}

func TestFilesOfAnotherStorageHaveNoURL(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	owner := uuid.NewV7()
	v := f.upload(t, owner, "avatar", "a.png", _png)

	moved := f.rows.files[v.File.ID]
	moved.Storage = "s3"
	f.rows.files[v.File.ID] = moved

	urls, err := f.svc.URLs(t.Context(), []uuid.UUID{v.File.ID})
	require.NoError(t, err)
	assert.Empty(t, urls)

	got, err := f.svc.Get(t.Context(), owner, v.File.ID)
	require.NoError(t, err)
	assert.Empty(t, got.URL)
}
