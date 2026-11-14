// Package storage keeps uploaded files in ./files (multer diskStorage) or in an S3 bucket
// (multer-s3), and signs S3 URLs for an hour.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_signedURLExpires = time.Hour
	_dirMode          = 0o755
	_fileMode         = 0o644
)

var errNotS3 = errors.New("storage: the local driver has no signed URLs")

// Local stores files under root.
type Local struct {
	root string
}

// NewLocal returns the local driver storing under root.
func NewLocal(root string) *Local {
	return &Local{root: root}
}

var _ repo.FileStorage = (*Local)(nil)

// Local implements repo.FileStorage.
func (*Local) Local() bool { return true }

// LocalFile is root/<base name of path>.
func (l *Local) LocalFile(p string) string {
	return filepath.Join(l.root, path.Base(p))
}

// NewSink implements repo.FileStorage.
func (l *Local) NewSink(key, _ string) (repo.Sink, error) {
	return &localSink{key: key, path: filepath.Join(l.root, key), dir: l.root, file: nil}, nil
}

// Delete removes the file, ignoring a missing one.
func (l *Local) Delete(_ context.Context, p string) error {
	if err := os.Remove(l.LocalFile(p)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: delete: %w", err)
	}

	return nil
}

// PresignGet implements repo.FileStorage.
func (*Local) PresignGet(context.Context, string) (string, error) { return "", errNotS3 }

// PresignPut implements repo.FileStorage.
func (*Local) PresignPut(context.Context, string, int64) (string, error) { return "", errNotS3 }

type localSink struct {
	key, path, dir string
	file           *os.File
}

func (s *localSink) Key() string { return s.key }

func (s *localSink) open() (*os.File, error) {
	if s.file != nil {
		return s.file, nil
	}

	if err := os.MkdirAll(s.dir, _dirMode); err != nil {
		return nil, fmt.Errorf("storage: mkdir: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, _fileMode)
	if err != nil {
		return nil, fmt.Errorf("storage: create: %w", err)
	}

	s.file = f

	return f, nil
}

func (s *localSink) Write(p []byte) (int, error) {
	f, err := s.open()
	if err != nil {
		return 0, err
	}

	return f.Write(p) //nolint:wrapcheck // os error
}

func (s *localSink) Commit(context.Context) error {
	f, err := s.open()
	if err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("storage: close: %w", err)
	}

	return nil
}

func (s *localSink) Discard(context.Context) {
	if s.file != nil {
		_ = s.file.Close()
	}

	_ = os.Remove(s.path)
}

// S3Config is the bucket configuration; Endpoint selects an S3-compatible service.
type S3Config struct {
	Bucket          string
	Region          string
	Endpoint        string
	ForcePathStyle  bool
	AccessKeyID     string
	SecretAccessKey string
}

// S3 stores files in a bucket.
type S3 struct {
	bucket  string
	client  *s3.Client
	presign *s3.PresignClient
}

// NewS3 returns the S3 driver.
func NewS3(cfg *S3Config) *S3 {
	client := s3.New(s3.Options{ //nolint:exhaustruct // SDK defaults
		Region:       cfg.Region,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		UsePathStyle: cfg.Endpoint != "" && cfg.ForcePathStyle,
		BaseEndpoint: endpoint(cfg.Endpoint),
	})

	return &S3{bucket: cfg.Bucket, client: client, presign: s3.NewPresignClient(client)}
}

func endpoint(e string) *string {
	if e == "" {
		return nil
	}

	return aws.String(e)
}

var _ repo.FileStorage = (*S3)(nil)

// Local implements repo.FileStorage.
func (*S3) Local() bool { return false }

// LocalFile implements repo.FileStorage.
func (*S3) LocalFile(string) string { return "" }

// NewSink buffers the upload in a temporary file and puts it on Commit.
func (s *S3) NewSink(key, contentType string) (repo.Sink, error) {
	tmp, err := os.CreateTemp("", "upload-*")
	if err != nil {
		return nil, fmt.Errorf("storage: temp file: %w", err)
	}

	return &s3Sink{s3: s, key: key, contentType: contentType, tmp: tmp, committed: false}, nil
}

// Delete removes the object.
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}) //nolint:exhaustruct // optional fields
	if err != nil {
		return fmt.Errorf("storage: delete object: %w", err)
	}

	return nil
}

// PresignGet signs a GET of key for an hour.
func (s *S3) PresignGet(ctx context.Context, key string) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}, //nolint:exhaustruct // optional fields
		s3.WithPresignExpires(_signedURLExpires))
	if err != nil {
		return "", fmt.Errorf("storage: presign get: %w", err)
	}

	return req.URL, nil
}

// PresignPut signs a PUT of key with its content length for an hour.
func (s *S3) PresignPut(ctx context.Context, key string, length int64) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{ //nolint:exhaustruct // optional fields
		Bucket: aws.String(s.bucket), Key: aws.String(key), ContentLength: aws.Int64(length),
	}, s3.WithPresignExpires(_signedURLExpires))
	if err != nil {
		return "", fmt.Errorf("storage: presign put: %w", err)
	}

	return req.URL, nil
}

type s3Sink struct {
	s3          *S3
	key         string
	contentType string
	tmp         *os.File
	committed   bool
}

func (s *s3Sink) Key() string { return s.key }

func (s *s3Sink) Write(p []byte) (int, error) {
	return s.tmp.Write(p) //nolint:wrapcheck // os error
}

func (s *s3Sink) cleanup() {
	_ = s.tmp.Close()
	_ = os.Remove(s.tmp.Name())
}

func (s *s3Sink) Commit(ctx context.Context) error {
	defer s.cleanup()

	size, err := s.tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("storage: size: %w", err)
	}

	if _, err := s.tmp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("storage: rewind: %w", err)
	}

	_, err = s.s3.client.PutObject(ctx, &s3.PutObjectInput{ //nolint:exhaustruct // optional fields
		Bucket: aws.String(s.s3.bucket), Key: aws.String(s.key), Body: s.tmp,
		ContentType: aws.String(s.contentType), ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("storage: put object: %w", err)
	}

	s.committed = true

	return nil
}

func (s *s3Sink) Discard(ctx context.Context) {
	s.cleanup()

	if s.committed {
		_ = s.s3.Delete(context.WithoutCancel(ctx), s.key)
	}
}
