// Package storage keeps file contents on local disk or in an S3-compatible bucket.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

const (
	_dirMode    = 0o750
	_fileMode   = 0o640
	_partSuffix = ".part"
)

var (
	errKey       = errors.New("storage: object key must be a plain file name")
	errNoPresign = errors.New("storage: the local driver has no presigned URLs")
)

func checkKey(key string) error {
	if key == "" || key != filepath.Base(key) || key == "." || key == ".." {
		return errKey
	}

	return nil
}

// Local stores files under root.
type Local struct {
	root string
}

// NewLocal returns the local driver storing under root.
func NewLocal(root string) *Local {
	return &Local{root: root}
}

var _ repo.FileStorage = (*Local)(nil)

// Name implements repo.FileStorage.
func (*Local) Name() string { return repo.StorageLocal }

// NewSink writes to <key>.part and renames it to key on Commit.
func (l *Local) NewSink(key string) (repo.Sink, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(l.root, _dirMode); err != nil {
		return nil, fmt.Errorf("storage: mkdir: %w", err)
	}

	part := filepath.Join(l.root, key+_partSuffix)

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_EXCL, _fileMode)
	if err != nil {
		return nil, fmt.Errorf("storage: create: %w", err)
	}

	return &localSink{file: f, part: part, path: filepath.Join(l.root, key)}, nil
}

// Open implements repo.FileStorage.
func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	f, err := os.Open(filepath.Join(l.root, key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, repo.ErrObjectNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("storage: open: %w", err)
	}

	return f, nil
}

// Delete implements repo.FileStorage.
func (l *Local) Delete(_ context.Context, key string) error {
	if err := checkKey(key); err != nil {
		return err
	}

	if err := os.Remove(filepath.Join(l.root, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: delete: %w", err)
	}

	return nil
}

// PresignGet implements repo.FileStorage; local files are served by getFileContent.
func (*Local) PresignGet(context.Context, string, string, bool, time.Duration) (string, error) {
	return "", errNoPresign
}

type localSink struct {
	file       *os.File
	part, path string
}

func (s *localSink) Write(p []byte) (int, error) {
	return s.file.Write(p) //nolint:wrapcheck // os error
}

func (s *localSink) Commit(context.Context, string) error {
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("storage: close: %w", err)
	}

	if err := os.Rename(s.part, s.path); err != nil {
		return fmt.Errorf("storage: rename: %w", err)
	}

	return nil
}

func (s *localSink) Discard(context.Context) {
	_ = s.file.Close()
	_ = os.Remove(s.part)
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
		UsePathStyle: cfg.ForcePathStyle,
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

// Name implements repo.FileStorage.
func (*S3) Name() string { return repo.StorageS3 }

// NewSink buffers the upload in a temporary file and puts the object on Commit.
func (s *S3) NewSink(key string) (repo.Sink, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp("", "upload-*")
	if err != nil {
		return nil, fmt.Errorf("storage: temp file: %w", err)
	}

	return &s3Sink{s3: s, key: key, tmp: tmp}, nil
}

// Open implements repo.FileStorage.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}) //nolint:exhaustruct // optional fields
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "NoSuchKey" {
			return nil, repo.ErrObjectNotFound
		}

		return nil, fmt.Errorf("storage: get object: %w", err)
	}

	return out.Body, nil
}

// Delete implements repo.FileStorage; S3 reports no error for a missing key.
func (s *S3) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}) //nolint:exhaustruct // optional fields
	if err != nil {
		return fmt.Errorf("storage: delete object: %w", err)
	}

	return nil
}

// PresignGet implements repo.FileStorage.
func (s *S3) PresignGet(ctx context.Context, key, name string, attachment bool, ttl time.Duration) (string, error) {
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)} //nolint:exhaustruct // optional fields
	if attachment {
		in.ResponseContentDisposition = aws.String(mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	}

	req, err := s.presign.PresignGetObject(ctx, in, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign get: %w", err)
	}

	return req.URL, nil
}

type s3Sink struct {
	s3        *S3
	key       string
	tmp       *os.File
	committed bool
}

func (s *s3Sink) Write(p []byte) (int, error) {
	return s.tmp.Write(p) //nolint:wrapcheck // os error
}

func (s *s3Sink) cleanup() {
	_ = s.tmp.Close()
	_ = os.Remove(s.tmp.Name())
}

func (s *s3Sink) Commit(ctx context.Context, contentType string) error {
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
		ContentType: aws.String(contentType), ContentLength: aws.Int64(size),
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
