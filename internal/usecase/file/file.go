// Package file uploads files, signs their URLs and serves local contents (platform conventions
// §11.5).
package file

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
)

const (
	_sniffBytes     = 512
	_maxPurposeSize = 64
	_maxNameRunes   = 255
	_defaultName    = "file"
	_fieldFile      = "file"
	_fieldPurpose   = "purpose"
)

var errSignature = errors.New("file: signature does not match")

// Config signs the getFileContent URLs of local files with Secret; URLs live for TTL.
type Config struct {
	Secret []byte
	TTL    time.Duration
}

// Service implements usecase.Files.
type Service struct {
	files   repo.Files
	storage repo.FileStorage
	cfg     Config
	log     *slog.Logger
	now     func() time.Time
}

var _ usecase.Files = (*Service)(nil)

// New returns a Service; now nil is time.Now.
func New(files repo.Files, storage repo.FileStorage, cfg Config, log *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}

	return &Service{files: files, storage: storage, cfg: cfg, log: log, now: now}
}

// staged is an uploaded file written to its sink but not committed.
type staged struct {
	sink        repo.Sink
	key         string
	name        string
	contentType string
	size        int64
	sum         []byte
}

// Upload implements usecase.Files: the parts stream to storage. With purpose first the file is
// held to that purpose's limit while it is written; with the file first, to the largest limit,
// and checked once purpose arrives. A rejected file is removed.
func (s *Service) Upload(ctx context.Context, owner uuid.UUID, form usecase.Form) (*usecase.FileView, error) {
	purpose, file, err := s.readForm(ctx, form)

	defer func() {
		if file != nil {
			file.sink.Discard(ctx)
		}
	}()

	if err != nil {
		return nil, err
	}

	if err := check(purpose, file); err != nil {
		return nil, err
	}

	f, err := s.store(ctx, owner, purpose, file)
	if err != nil {
		return nil, err
	}

	file = nil

	return s.view(ctx, f)
}

// readForm reads the purpose and stages the file; the staged file is returned with any error so
// that the caller discards it.
func (s *Service) readForm(ctx context.Context, form usecase.Form) (purpose *entity.Purpose, file *staged, err error) {
	for {
		part, err := form.NextPart()
		if errors.Is(err, io.EOF) {
			return purpose, file, nil
		}

		if err != nil {
			return purpose, file, readError(err)
		}

		switch part.FormName() {
		case _fieldPurpose:
			purpose, err = readPurpose(part)
		case _fieldFile:
			file, err = s.stageOnce(ctx, part, purpose, file)
		}

		if err != nil {
			return purpose, file, err
		}
	}
}

// stageOnce stages the file part unless one was staged already.
func (s *Service) stageOnce(ctx context.Context, part *multipart.Part, purpose *entity.Purpose, file *staged) (*staged, error) {
	if file != nil {
		return file, entity.Invalid(_fieldFile, entity.RuleMaxItems, map[string]any{"maxItems": 1})
	}

	return s.stage(ctx, part, purpose)
}

func readError(err error) error {
	if errors.Is(err, entity.New(entity.KeyFileTooLarge)) {
		return err
	}

	return entity.Wrap(entity.KeyBadRequest, err)
}

func purposeNames() []any {
	var names []any

	for _, p := range entity.Purposes() {
		names = append(names, p.Name)
	}

	return names
}

func readPurpose(part *multipart.Part) (*entity.Purpose, error) {
	raw, err := io.ReadAll(io.LimitReader(part, _maxPurposeSize+1))
	if err != nil {
		return nil, readError(err)
	}

	p, ok := entity.PurposeOf(string(raw))
	if !ok {
		return nil, entity.Invalid(_fieldPurpose, entity.RuleEnum, map[string]any{"values": purposeNames()})
	}

	return &p, nil
}

func check(purpose *entity.Purpose, file *staged) error {
	var missing []entity.FieldError

	if file == nil {
		missing = append(missing, entity.FieldError{Field: _fieldFile, Rule: entity.RuleRequired, Params: nil})
	}

	if purpose == nil {
		missing = append(missing, entity.FieldError{Field: _fieldPurpose, Rule: entity.RuleRequired, Params: nil})
	}

	if len(missing) > 0 {
		return &entity.ValidationError{Errors: missing}
	}

	if file.size > purpose.MaxSize {
		return entity.New(entity.KeyFileTooLarge)
	}

	for _, t := range purpose.Types {
		if t == file.contentType {
			return nil
		}
	}

	return entity.New(entity.KeyFileTypeNotAllowed)
}

// fileName is the base name of raw as valid UTF-8 without control characters, at most
// _maxNameRunes long.
func fileName(raw string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, strings.ToValidUTF8(raw, string(utf8.RuneError)))

	name := strings.TrimSpace(filepath.Base(strings.ReplaceAll(clean, `\`, "/")))
	if name == "" || name == "." || name == "/" {
		return _defaultName
	}

	if utf8.RuneCountInString(name) > _maxNameRunes {
		name = string([]rune(name)[:_maxNameRunes])
	}

	return name
}

// stage writes part to a new sink, detecting its type from the first bytes and hashing it.
func (s *Service) stage(ctx context.Context, part *multipart.Part, purpose *entity.Purpose) (*staged, error) {
	limit := entity.MaxFileSize()
	if purpose != nil {
		limit = purpose.MaxSize
	}

	key := uuid.NewV7().String()

	sink, err := s.storage.NewSink(key)
	if err != nil {
		return nil, fmt.Errorf("file: %w", err)
	}

	sum := sha256.New()

	head, n, err := copyFile(io.MultiWriter(sink, sum), part, limit)
	if err != nil {
		sink.Discard(ctx)

		return nil, err
	}

	return &staged{
		sink: sink, key: key, name: fileName(part.FileName()), contentType: detect(head), size: n, sum: sum.Sum(nil),
	}, nil
}

func detect(head []byte) string {
	t := http.DetectContentType(head)
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}

	return t
}

// copyFile copies at most limit bytes and returns the first _sniffBytes of them; a failed read is
// the client's, a failed write the storage's.
func copyFile(dst io.Writer, src io.Reader, limit int64) (head []byte, n int64, err error) {
	var buf bytes.Buffer

	out := &trackedWriter{w: dst, err: nil}

	n, err = io.Copy(out, io.TeeReader(io.LimitReader(src, limit+1), &sniffer{buf: &buf}))

	switch {
	case out.err != nil:
		return nil, 0, fmt.Errorf("file: store: %w", out.err)
	case err != nil:
		return nil, 0, readError(err)
	}

	if n > limit {
		return nil, 0, entity.New(entity.KeyFileTooLarge)
	}

	return buf.Bytes(), n, nil
}

// trackedWriter keeps the error of the writer it wraps.
type trackedWriter struct {
	w   io.Writer
	err error
}

func (t *trackedWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if err != nil {
		t.err = err
	}

	return n, err //nolint:wrapcheck // kept in t.err
}

// sniffer keeps the first _sniffBytes written to it.
type sniffer struct {
	buf *bytes.Buffer
}

func (s *sniffer) Write(p []byte) (int, error) {
	if room := _sniffBytes - s.buf.Len(); room > 0 {
		s.buf.Write(p[:min(room, len(p))])
	}

	return len(p), nil
}

func (s *Service) store(ctx context.Context, owner uuid.UUID, purpose *entity.Purpose, file *staged) (*entity.File, error) {
	if err := file.sink.Commit(ctx, file.contentType); err != nil {
		return nil, fmt.Errorf("file: %w", err)
	}

	f := &entity.File{
		ID: uuid.NewV7(), OwnerSub: owner, Purpose: purpose.Name, Name: file.name, ContentType: file.contentType,
		Size: file.size, SHA256: file.sum, Storage: s.storage.Name(), ObjectKey: file.key, CreatedAt: time.Time{},
	}

	if err := s.files.Create(ctx, f); err != nil {
		if delErr := s.storage.Delete(context.WithoutCancel(ctx), file.key); delErr != nil {
			s.log.WarnContext(ctx, "orphan object left in storage", "key", file.key, "error", delErr.Error())
		}

		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return f, nil
}

// Get implements usecase.Files: only the owner sees a file.
func (s *Service) Get(ctx context.Context, owner, id uuid.UUID) (*usecase.FileView, error) {
	f, found, err := s.files.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found || f.OwnerSub != owner {
		return nil, entity.New(entity.KeyNotFound)
	}

	return s.view(ctx, f)
}

func (s *Service) view(ctx context.Context, f *entity.File) (*usecase.FileView, error) {
	url, err := s.url(ctx, f)
	if err != nil {
		return nil, err
	}

	return &usecase.FileView{File: *f, URL: url}, nil
}

// url is the getFileContent URL of a local file, or a presigned GET of an S3 object; "" for a file
// kept by another driver than the configured one.
func (s *Service) url(ctx context.Context, f *entity.File) (string, error) {
	if f.Storage != s.storage.Name() {
		return "", nil
	}

	if f.Storage != repo.StorageLocal {
		url, err := s.storage.PresignGet(ctx, f.ObjectKey, f.Name, !f.Image(), s.cfg.TTL)
		if err != nil {
			return "", fmt.Errorf("file: %w", err)
		}

		return url, nil
	}

	expires := strconv.FormatInt(s.now().Add(s.cfg.TTL).Unix(), 10)

	return "/api/v1/files/" + f.ID.String() + "/content?expires=" + expires + "&signature=" + s.sign(f.ID, expires), nil
}

func (s *Service) mac() hash.Hash {
	return hmac.New(sha256.New, s.cfg.Secret)
}

func (s *Service) sign(id uuid.UUID, expires string) string {
	m := s.mac()
	_, _ = m.Write([]byte(id.String() + "." + expires))

	return hex.EncodeToString(m.Sum(nil))
}

func (s *Service) verify(id uuid.UUID, expires, signature string) error {
	at, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || s.now().Unix() > at {
		return errSignature
	}

	if !hmac.Equal([]byte(signature), []byte(s.sign(id, expires))) {
		return errSignature
	}

	return nil
}

// Content implements usecase.Files: a missing, deleted, non-local or badly signed file is
// common.notFound.
func (s *Service) Content(ctx context.Context, id uuid.UUID, expires, signature string) (f *entity.File, body io.ReadCloser, err error) {
	if err := s.verify(id, expires, signature); err != nil {
		return nil, nil, entity.Wrap(entity.KeyNotFound, err)
	}

	f, found, err := s.files.Get(ctx, id)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found || f.Storage != s.storage.Name() || f.Storage != repo.StorageLocal {
		return nil, nil, entity.New(entity.KeyNotFound)
	}

	body, err = s.storage.Open(ctx, f.ObjectKey)
	if errors.Is(err, repo.ErrObjectNotFound) {
		return nil, nil, entity.Wrap(entity.KeyNotFound, err)
	}

	if err != nil {
		return nil, nil, fmt.Errorf("file: %w", err)
	}

	return f, body, nil
}

// URLs returns the URLs of the files of ids that exist in the configured storage; the others are
// left out.
func (s *Service) URLs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	out := map[uuid.UUID]string{}
	if len(ids) == 0 {
		return out, nil
	}

	files, err := s.files.ListByID(ctx, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	for i := range files {
		url, err := s.url(ctx, &files[i])
		if err != nil {
			return nil, err
		}

		if url != "" {
			out[files[i].ID] = url
		}
	}

	return out, nil
}

// IsAvatar reports whether id is an avatar file of owner.
func (s *Service) IsAvatar(ctx context.Context, owner, id uuid.UUID) (bool, error) {
	f, found, err := s.files.Get(ctx, id)
	if err != nil {
		return false, err //nolint:wrapcheck // repo errors are wrapped
	}

	return found && f.OwnerSub == owner && f.Purpose == entity.PurposeAvatar, nil
}

// Purge implements repo.Purger: the objects of the owner, then their rows.
func (s *Service) Purge(ctx context.Context, owner uuid.UUID) error {
	files, err := s.files.ListOwner(ctx, owner)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	for i := range files {
		if files[i].Storage != s.storage.Name() {
			s.log.WarnContext(ctx, "file left in another storage", "file_id", files[i].ID, "storage", files[i].Storage)

			continue
		}

		if err := s.storage.Delete(ctx, files[i].ObjectKey); err != nil {
			return fmt.Errorf("file: purge: %w", err)
		}
	}

	return s.files.DeleteOwner(ctx, owner) //nolint:wrapcheck // repo errors are wrapped
}
