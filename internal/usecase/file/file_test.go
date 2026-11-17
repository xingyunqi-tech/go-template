package file_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	filemocks "github.com/xingyunqi-tech/go-template/internal/usecase/file/mocks"
)

var errStore = errors.New("store down")

type sqlStateError string

func (e sqlStateError) Error() string    { return "sql error " + string(e) }
func (e sqlStateError) SQLState() string { return string(e) }

type deps struct {
	files   *mocks.MockFiles
	storage *mocks.MockFileStorage
	tx      *filemocks.MockTransactions
}

func newService(t *testing.T) (*file.Service, deps) {
	t.Helper()

	ctrl := gomock.NewController(t)
	d := deps{files: mocks.NewMockFiles(ctrl), storage: mocks.NewMockFileStorage(ctrl), tx: filemocks.NewMockTransactions(ctrl)}
	cfg := file.Config{
		APIPrefix: "api", BackendDomain: "https://api.example.com",
		MaxSizes: map[file.Purpose]int64{file.PurposeImage: 100, file.PurposeExport: 1000},
	}

	return file.New(cfg, d.files, d.storage, d.tx, slog.New(slog.DiscardHandler)), d
}

func ptr[T any](v T) *T { return &v }

func TestParsePurpose(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		values  []string
		present bool
		want    file.Purpose
		ok      bool
	}{
		{name: "absent", present: false, want: file.PurposeImage, ok: true},
		{name: "empty", values: []string{""}, present: true, want: file.PurposeImage, ok: true},
		{name: "known", values: []string{"audio"}, present: true, want: file.PurposeAudio, ok: true},
		{name: "unknown", values: []string{"video"}, present: true, want: "video", ok: false},
		{name: "repeated", values: []string{"image", "audio"}, present: true, want: "", ok: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := file.ParsePurpose(tc.values, tc.present)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.ok, ok)
		})
	}
}

func TestMimeTypeIsChosenByLowerCasedExtension(t *testing.T) {
	t.Parallel()

	mime, ok := file.MimeType(file.PurposeImage, "a.b.PNG")
	assert.True(t, ok)
	assert.Equal(t, "image/png", mime)

	_, ok = file.MimeType(file.PurposePrivateImage, "a.gif")
	assert.False(t, ok)

	_, ok = file.MimeType(file.PurposeImage, "noext")
	assert.False(t, ok)

	assert.Equal(t, "audio/mp4", file.AllMimeTypes()["m4a"])
}

func TestRandomKeyKeepsLowerCasedExtension(t *testing.T) {
	t.Parallel()

	assert.Regexp(t, `^[0-9a-f]{21}\.jpg$`, file.RandomKey("Photo.JPG"))
	assert.Regexp(t, `^[0-9a-f]{21}\.noext$`, file.RandomKey("NoExt"))
}

func TestMaxFileSizeIsLargestPurposeLimit(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)
	assert.Equal(t, int64(1000), svc.MaxFileSize())
	assert.Equal(t, int64(100), svc.MaxSize(file.PurposeImage))
}

func TestStoreRejectsMissingAndOversizedFiles(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t)

	_, err := svc.Store(context.Background(), nil, file.PurposeImage, 3)
	require.ErrorIs(t, err, file.ErrSelectFile)

	_, err = svc.Store(context.Background(), &file.Received{Name: "a.png", Size: 101, Key: "k.png"}, file.PurposeImage, 3)

	var e *entity.Error
	require.ErrorAs(t, err, &e)
	assert.Equal(t, entity.StatusPayloadTooLarge, e.Status)
}

func TestStoreRecordsLocalFileUnderURLPath(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.storage.EXPECT().Local().Return(true)
	d.files.EXPECT().Create(gomock.Any(), &entity.File{
		Path: "/api/v1/files/k.png", Visibility: entity.VisibilityPublic, MimeType: ptr("image/png"), Size: ptr(10), OwnerUserID: ptr(3),
	}).DoAndReturn(func(_ context.Context, f *entity.File) (*entity.File, error) { return f, nil })

	_, err := svc.Store(context.Background(), &file.Received{Name: "a.png", Size: 10, Key: "k.png"}, file.PurposeImage, 3)
	require.NoError(t, err)
}

func TestStoreSetsOwnerOnlyForNonContentAssets(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.storage.EXPECT().Local().Return(false)
	d.files.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, f *entity.File) (*entity.File, error) {
		assert.Equal(t, "k.pdf", f.Path)
		assert.Equal(t, entity.VisibilityPrivate, f.Visibility)
		assert.NotNil(t, f.OwnerUserID)

		return f, nil
	})

	_, err := svc.Store(context.Background(), &file.Received{Name: "a.pdf", Size: 10, Key: "k.pdf"}, file.PurposeExport, 3)
	require.NoError(t, err)

	svc, d = newService(t)
	d.storage.EXPECT().Local().Return(false)
	d.files.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, f *entity.File) (*entity.File, error) {
		assert.Nil(t, f.OwnerUserID)

		return f, nil
	})

	_, err = svc.Store(context.Background(), &file.Received{Name: "a.png", Size: 0, Key: "k.png"}, file.PurposeContentImage, 3)
	require.NoError(t, err)
}

func TestPresignCreatesRecordAndReturnsSignedURL(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	d.storage.EXPECT().PresignPut(gomock.Any(), gomock.Any(), int64(50)).Return("https://s3/put", nil)
	d.files.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, f *entity.File) (*entity.File, error) {
		assert.Regexp(t, `^[0-9a-f]{21}\.png$`, f.Path)
		assert.Equal(t, ptr(50), f.Size)

		return f, nil
	})

	f, signed, err := svc.Presign(context.Background(), "a.png", 50, file.PurposeImage, 3)
	require.NoError(t, err)
	assert.Equal(t, "https://s3/put", signed)
	assert.Equal(t, ptr(3), f.OwnerUserID)
}

func TestPresignErrors(t *testing.T) {
	t.Parallel()

	t.Run("type not allowed", func(t *testing.T) {
		t.Parallel()

		svc, _ := newService(t)
		_, _, err := svc.Presign(context.Background(), "a.exe", 1, file.PurposeImage, 3)
		require.ErrorIs(t, err, file.ErrFileType)
	})

	t.Run("too large", func(t *testing.T) {
		t.Parallel()

		svc, _ := newService(t)
		_, _, err := svc.Presign(context.Background(), "a.png", 100.5, file.PurposeImage, 3)

		var e *entity.Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, entity.StatusPayloadTooLarge, e.Status)
	})

	t.Run("signing fails", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.storage.EXPECT().PresignPut(gomock.Any(), gomock.Any(), gomock.Any()).Return("", errStore)

		_, _, err := svc.Presign(context.Background(), "a.png", 1, file.PurposeImage, 3)
		require.ErrorIs(t, err, errStore)
	})

	t.Run("fractional size", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.storage.EXPECT().PresignPut(gomock.Any(), gomock.Any(), int64(1)).Return("https://s3/put", nil)

		_, _, err := svc.Presign(context.Background(), "a.png", 1.5, file.PurposeImage, 3)
		require.ErrorIs(t, err, file.ErrSizeNotInteger)
	})

	t.Run("record fails", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.storage.EXPECT().PresignPut(gomock.Any(), gomock.Any(), gomock.Any()).Return("https://s3/put", nil)
		d.files.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errStore)

		_, _, err := svc.Presign(context.Background(), "a.png", 1, file.PurposeImage, 3)
		require.ErrorIs(t, err, errStore)
	})
}

func TestFindAccessible(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	cases := []struct {
		name string
		row  *entity.File
		err  error
		want error
	}{
		{name: "public file of another user", row: &entity.File{Visibility: entity.VisibilityPublic, OwnerUserID: ptr(4)}},
		{name: "own private file", row: &entity.File{Visibility: entity.VisibilityPrivate, OwnerUserID: ptr(3)}},
		{name: "private file of another user", row: &entity.File{Visibility: entity.VisibilityPrivate, OwnerUserID: ptr(4)}, want: file.ErrNotAccessible},
		{name: "private file without owner", row: &entity.File{Visibility: entity.VisibilityPrivate}, want: file.ErrNotAccessible},
		{name: "missing", want: file.ErrNotFound},
		{name: "repo error", err: errStore, want: errStore},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, d := newService(t)
			d.files.EXPECT().Get(gomock.Any(), id).Return(tc.row, tc.err)

			got, err := svc.FindAccessible(context.Background(), id, 3)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)

				return
			}

			require.NoError(t, err)
			assert.Same(t, tc.row, got)
		})
	}
}

func TestFindPublicByNameTreatsPrivateFilesAsMissing(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	public := &entity.File{Visibility: entity.VisibilityPublic}
	d.files.EXPECT().GetByPath(gomock.Any(), "/api/v1/files/a.png").Return(public, nil)
	d.files.EXPECT().GetByPath(gomock.Any(), "/api/v1/files/b.png").Return(&entity.File{Visibility: entity.VisibilityPrivate}, nil)
	d.files.EXPECT().GetByPath(gomock.Any(), "/api/v1/files/c.png").Return(nil, errStore)

	got, err := svc.FindPublicByName(context.Background(), "a.png")
	require.NoError(t, err)
	assert.Same(t, public, got)

	_, err = svc.FindPublicByName(context.Background(), "b.png")
	require.ErrorIs(t, err, file.ErrNotFound)

	_, err = svc.FindPublicByName(context.Background(), "c.png")
	require.ErrorIs(t, err, errStore)
}

func TestURL(t *testing.T) {
	t.Parallel()

	id := uuid.New()

	t.Run("local public", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.storage.EXPECT().Local().Return(true)
		assert.Equal(t, "https://api.example.com/api/v1/files/k.png",
			svc.URL(&entity.File{Path: "/api/v1/files/k.png", Visibility: entity.VisibilityPublic}))
	})

	t.Run("local private", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.storage.EXPECT().Local().Return(true)
		assert.Equal(t, "https://api.example.com/api/v1/files/private/"+id.String(),
			svc.URL(&entity.File{ID: id, Path: "/api/v1/files/k.png", Visibility: entity.VisibilityPrivate}))
	})

	t.Run("s3 signed", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.storage.EXPECT().Local().Return(false)
		d.storage.EXPECT().PresignGet(gomock.Any(), "k.png").Return("https://s3/get", nil)
		assert.Equal(t, "https://s3/get", svc.URL(&entity.File{Path: "k.png"}))
	})

	t.Run("s3 signing failure", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.storage.EXPECT().Local().Return(false)
		d.storage.EXPECT().PresignGet(gomock.Any(), "k.png").Return("", errStore)
		assert.Empty(t, svc.URL(&entity.File{Path: "k.png"}))
	})
}

func runInTx(d deps) *filemocks.MockTransactionsInTxCall {
	return d.tx.EXPECT().InTx(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
		return fn(ctx)
	})
}

func filesOf(n int) []entity.File {
	out := make([]entity.File, n)
	for i := range out {
		out[i] = entity.File{ID: uuid.New(), Path: fmt.Sprintf("k%d.png", i)}
	}

	return out
}

func TestCleanerPagesThroughOwnedFiles(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	first, second := filesOf(100), filesOf(1)

	d.files.EXPECT().ListByOwner(gomock.Any(), 3, nil, 100).Return(first, nil)
	d.files.EXPECT().ListByOwner(gomock.Any(), 3, &first[99].ID, 100).Return(second, nil)
	runInTx(d).Times(101)
	d.files.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).Times(101)
	d.storage.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).Times(101)

	c := file.NewCleaner(svc)
	assert.Equal(t, "files", c.Name())
	assert.Equal(t, 1000, c.Order())
	require.NoError(t, c.Clean(context.Background(), 3))
}

func TestCleanerKeepsReferencedFiles(t *testing.T) {
	t.Parallel()

	svc, d := newService(t)
	files := filesOf(2)

	d.files.EXPECT().ListByOwner(gomock.Any(), 3, nil, 100).Return(files, nil)
	runInTx(d).Times(2)
	d.files.EXPECT().Delete(gomock.Any(), files[0].ID).Return(fmt.Errorf("delete: %w", sqlStateError("23503")))
	d.files.EXPECT().Delete(gomock.Any(), files[1].ID).Return(nil)
	d.storage.EXPECT().Delete(gomock.Any(), files[1].Path).Return(nil)

	require.NoError(t, file.NewCleaner(svc).Clean(context.Background(), 3))
}

func TestCleanerStopsOnOtherErrors(t *testing.T) {
	t.Parallel()

	t.Run("list", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.files.EXPECT().ListByOwner(gomock.Any(), 3, nil, 100).Return(nil, errStore)
		require.ErrorIs(t, file.NewCleaner(svc).Clean(context.Background(), 3), errStore)
	})

	t.Run("storage", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.files.EXPECT().ListByOwner(gomock.Any(), 3, nil, 100).Return(filesOf(2), nil)
		runInTx(d)
		d.files.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil)
		d.storage.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(errStore)
		require.ErrorIs(t, file.NewCleaner(svc).Clean(context.Background(), 3), errStore)
	})

	t.Run("other sql state", func(t *testing.T) {
		t.Parallel()

		svc, d := newService(t)
		d.files.EXPECT().ListByOwner(gomock.Any(), 3, nil, 100).Return(filesOf(1), nil)
		runInTx(d)
		d.files.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(sqlStateError("40001"))
		require.ErrorAs(t, file.NewCleaner(svc).Clean(context.Background(), 3), new(sqlStateError))
	})
}
