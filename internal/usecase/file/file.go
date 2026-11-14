// Package file holds the nestjs upload purposes, file records and file URLs.
package file

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// Purpose is the upload purpose query parameter.
type Purpose string

// Upload purposes.
const (
	PurposeImage        Purpose = "image"
	PurposePrivateImage Purpose = "private-image"
	PurposeContentImage Purpose = "content-image"
	PurposeAudio        Purpose = "audio"
	PurposeImport       Purpose = "import"
	PurposeExport       Purpose = "export"
)

const (
	_keyLength       = 21
	_keyBytes        = 11
	_cleanerOrder    = 1000
	_cleanerBatch    = 100
	_foreignKeyError = "23503"
)

// Rule is the nestjs FILE_PURPOSE_RULES entry; content assets have no owner.
type Rule struct {
	Types        map[string]string
	Visibility   string
	Permission   string
	ContentAsset bool
}

func imageTypes() map[string]string {
	return map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp"}
}

// Rules returns the purpose rules.
func Rules() map[Purpose]Rule {
	return map[Purpose]Rule{
		PurposeImage: {Types: imageTypes(), Visibility: entity.VisibilityPublic, Permission: "", ContentAsset: false},
		PurposePrivateImage: {
			Types:      map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"},
			Visibility: entity.VisibilityPrivate, Permission: "", ContentAsset: false,
		},
		PurposeContentImage: {Types: imageTypes(), Visibility: entity.VisibilityPublic, Permission: entity.PermContentManage, ContentAsset: true},
		PurposeAudio: {
			Types:      map[string]string{"mp3": "audio/mpeg", "m4a": "audio/mp4"},
			Visibility: entity.VisibilityPublic, Permission: entity.PermContentManage, ContentAsset: true,
		},
		PurposeImport: {
			Types: map[string]string{
				"zip": "application/zip", "json": "application/json",
				"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			},
			Visibility: entity.VisibilityPrivate, Permission: entity.PermContentManage, ContentAsset: false,
		},
		PurposeExport: {
			Types: map[string]string{"pdf": "application/pdf"}, Visibility: entity.VisibilityPrivate,
			Permission: entity.PermContentManage, ContentAsset: false,
		},
	}
}

// ParsePurpose is nestjs parseFilePurpose: absent or empty is image; ok is false otherwise
// unless the value is a known purpose.
func ParsePurpose(values []string, present bool) (Purpose, bool) {
	switch {
	case !present:
		return PurposeImage, true
	case len(values) != 1:
		return "", false
	case values[0] == "":
		return PurposeImage, true
	}

	p := Purpose(values[0])
	_, ok := Rules()[p]

	return p, ok
}

// Extension is the lower-cased text after the last dot, or "" without a dot.
func Extension(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}

	return strings.ToLower(name[i+1:])
}

// MimeType is the purpose's content type of a file name, chosen by extension only.
func MimeType(p Purpose, name string) (string, bool) {
	t, ok := Rules()[p].Types[Extension(name)]

	return t, ok
}

// AllMimeTypes maps every allowed extension to its content type.
func AllMimeTypes() map[string]string {
	out := map[string]string{}

	for _, r := range Rules() {
		for ext, t := range r.Types {
			out[ext] = t
		}
	}

	return out
}

// RandomKey is the stored name: 21 lowercase hex characters and the original extension.
func RandomKey(name string) string {
	b := make([]byte, _keyBytes)
	_, _ = rand.Read(b)

	ext := name
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		ext = name[i+1:]
	}

	return hex.EncodeToString(b)[:_keyLength] + "." + strings.ToLower(ext)
}

// Errors of the upload rules.
var (
	ErrInvalidPurpose = entity.Invalid("purpose", "invalidFilePurpose")
	ErrFileType       = entity.Invalid("file", "cantUploadFileType")
	ErrSelectFile     = entity.Invalid("file", "selectFile")
	ErrNotFound       = entity.NotFoundKey("fileNotFound")
	ErrNotAccessible  = &entity.Error{Status: entity.StatusForbidden, Message: "", ErrorKey: "fileNotAccessible", Fields: nil}
)

// Config holds the driver-independent file settings.
type Config struct {
	APIPrefix     string
	BackendDomain string
	MaxSizes      map[Purpose]int64
}

// Transactions runs work in a transaction of its own.
type Transactions interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service manages file records.
type Service struct {
	cfg     Config
	files   repo.Files
	storage repo.FileStorage
	tx      Transactions
	log     *slog.Logger
}

// New returns a Service.
func New(cfg Config, files repo.Files, storage repo.FileStorage, tx Transactions, log *slog.Logger) *Service {
	return &Service{cfg: cfg, files: files, storage: storage, tx: tx, log: log}
}

// Storage is the configured driver.
func (s *Service) Storage() repo.FileStorage { return s.storage }

// MaxSize is the upload limit of a purpose.
func (s *Service) MaxSize(p Purpose) int64 { return s.cfg.MaxSizes[p] }

// MaxFileSize is the largest limit of all purposes, the multipart file limit.
func (s *Service) MaxFileSize() int64 {
	var m int64
	for _, v := range s.cfg.MaxSizes {
		m = max(m, v)
	}

	return m
}

// LocalURLPath is the stored path of a local file: /<API_PREFIX>/v1/files/<key>.
func (s *Service) LocalURLPath(key string) string {
	return "/" + s.cfg.APIPrefix + "/v1/files/" + key
}

func owner(p Purpose, userID int) *int {
	if Rules()[p].ContentAsset {
		return nil
	}

	return &userID
}

// Received is an upload stored under Key.
type Received struct {
	Name string
	Size int64
	Key  string
}

// Store records a received upload (nestjs Files{Local,S3}Service.create); nil means no file.
func (s *Service) Store(ctx context.Context, r *Received, p Purpose, userID int) (*entity.File, error) {
	if r == nil {
		return nil, ErrSelectFile
	}

	if r.Size > s.MaxSize(p) {
		return nil, entity.FileTooLarge()
	}

	mime, _ := MimeType(p, r.Name)
	size := int(r.Size)

	path := r.Key
	if s.storage.Local() {
		path = s.LocalURLPath(r.Key)
	}

	return s.files.Create(ctx, &entity.File{ //nolint:wrapcheck // repo errors are wrapped
		ID: uuid.UUID{}, Path: path, Visibility: Rules()[p].Visibility, MimeType: &mime, Size: &size, OwnerUserID: owner(p, userID),
	})
}

// Presign is the s3-presigned upload: the record is created now and the client PUTs the bytes.
func (s *Service) Presign(ctx context.Context, name string, size float64, p Purpose, userID int) (*entity.File, string, error) {
	mime, ok := MimeType(p, name)
	if !ok {
		return nil, "", ErrFileType
	}

	if size > float64(s.MaxSize(p)) {
		return nil, "", entity.FileTooLarge()
	}

	key := RandomKey(name)

	signed, err := s.storage.PresignPut(ctx, key, int64(size))
	if err != nil {
		return nil, "", err //nolint:wrapcheck // storage errors are wrapped
	}

	n := int(size)

	f, err := s.files.Create(ctx, &entity.File{
		ID: uuid.UUID{}, Path: key, Visibility: Rules()[p].Visibility, MimeType: &mime, Size: &n, OwnerUserID: owner(p, userID),
	})
	if err != nil {
		return nil, "", err //nolint:wrapcheck // repo errors are wrapped
	}

	return f, signed, nil
}

// FindAccessible returns a file the user may read: public files, or private files they own.
func (s *Service) FindAccessible(ctx context.Context, id uuid.UUID, userID int) (*entity.File, error) {
	f, err := s.files.Get(ctx, id)

	switch {
	case err != nil:
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	case f == nil:
		return nil, ErrNotFound
	case f.Visibility == entity.VisibilityPrivate && (f.OwnerUserID == nil || *f.OwnerUserID != userID):
		return nil, ErrNotAccessible
	}

	return f, nil
}

// FindPublicByName returns the public local file stored as name; private files count as missing.
func (s *Service) FindPublicByName(ctx context.Context, name string) (*entity.File, error) {
	f, err := s.files.GetByPath(ctx, s.LocalURLPath(name))
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if f == nil || f.Visibility != entity.VisibilityPublic {
		return nil, ErrNotFound
	}

	return f, nil
}

// URL is nestjs buildFileUrl: local public files at BACKEND_DOMAIN + path, local private files
// at the private route, S3 files as a signed GET URL.
func (s *Service) URL(f *entity.File) string {
	if !s.storage.Local() {
		u, err := s.storage.PresignGet(context.Background(), f.Path)
		if err != nil {
			s.log.Error("sign file url failed", "error", err.Error())
		}

		return u
	}

	if f.Visibility == entity.VisibilityPrivate {
		return s.cfg.BackendDomain + "/" + s.cfg.APIPrefix + "/v1/files/private/" + f.ID.String()
	}

	return s.cfg.BackendDomain + f.Path
}

// Cleaner deletes the files a removed user uploaded; files still referenced are kept.
type Cleaner struct {
	s *Service
}

// NewCleaner returns the files account cleaner (order 1000).
func NewCleaner(s *Service) *Cleaner { return &Cleaner{s: s} }

// Name implements accountcleanup.Cleaner.
func (*Cleaner) Name() string { return "files" }

// Order implements accountcleanup.Cleaner.
func (*Cleaner) Order() int { return _cleanerOrder }

// Clean implements accountcleanup.Cleaner.
func (c *Cleaner) Clean(ctx context.Context, userID int) error {
	var after *uuid.UUID

	for {
		batch, err := c.s.files.ListByOwner(ctx, userID, after, _cleanerBatch)
		if err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}

		for i := range batch {
			if err := c.remove(ctx, &batch[i]); err != nil {
				return err
			}
		}

		if len(batch) < _cleanerBatch {
			return nil
		}

		after = &batch[len(batch)-1].ID
	}
}

// remove deletes the row and then the stored object in one transaction.
func (c *Cleaner) remove(ctx context.Context, f *entity.File) error {
	err := c.s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := c.s.files.Delete(ctx, f.ID); err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}

		return c.s.storage.Delete(ctx, f.Path) //nolint:wrapcheck // storage errors are wrapped
	})

	var coded interface{ SQLState() string }
	if errors.As(err, &coded) && coded.SQLState() == _foreignKeyError {
		c.s.log.WarnContext(ctx, "file is still referenced and kept", "file_id", f.ID.String())

		return nil
	}

	return err
}
