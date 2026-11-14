//go:build integration

package testutil

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

// MinioImage is the S3-compatible server of the integration tests.
const MinioImage = "chainguard/minio@sha256:db3f583fa49121942168bd6383a773b2cde3384c9a99bf7193560b9a0f3a28fd"

// Minio is a running MinIO container with one bucket.
type Minio struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Client    *s3.Client
	container testcontainers.Container
}

// StartMinio starts MinIO and creates bucket; failures are returned, never skipped.
func StartMinio(ctx context.Context, bucket string) (*Minio, error) {
	UseDockerDesktopSocket()

	c, err := tcminio.Run(ctx, MinioImage)
	if err != nil {
		return nil, fmt.Errorf("testutil: start minio: %w", err)
	}

	addr, err := c.ConnectionString(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(c)

		return nil, fmt.Errorf("testutil: minio endpoint: %w", err)
	}

	m := &Minio{
		Endpoint: "http://" + addr, AccessKey: c.Username, SecretKey: c.Password, Bucket: bucket, Client: nil, container: c,
	}
	m.Client = s3.New(s3.Options{ //nolint:exhaustruct // SDK defaults
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(m.AccessKey, m.SecretKey, ""),
		BaseEndpoint: aws.String(m.Endpoint),
		UsePathStyle: true,
	})

	if _, err := m.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil { //nolint:exhaustruct // defaults
		_ = testcontainers.TerminateContainer(c)

		return nil, fmt.Errorf("testutil: create bucket: %w", err)
	}

	return m, nil
}

// Terminate stops and removes the container.
func (m *Minio) Terminate() error {
	if err := testcontainers.TerminateContainer(m.container); err != nil {
		return fmt.Errorf("testutil: terminate minio: %w", err)
	}

	return nil
}
