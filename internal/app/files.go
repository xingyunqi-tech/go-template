package app

import (
	"crypto/rand"
	"fmt"
	"log/slog"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/storage"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
)

const _ephemeralSecretBytes = 32

func newStorage(cfg *config.Storage) repo.FileStorage {
	if cfg.Driver == config.StorageS3 {
		return storage.NewS3(&storage.S3Config{
			Bucket: cfg.Bucket, Region: cfg.Region, Endpoint: cfg.Endpoint, ForcePathStyle: cfg.ForcePathStyle,
			AccessKeyID: cfg.AccessKeyID, SecretAccessKey: cfg.SecretAccessKey.Value(),
		})
	}

	return storage.NewLocal(cfg.LocalRoot)
}

// newFiles returns the file use case; without FILE_URL_SECRET, outside production, URLs are
// signed with a key generated at startup.
func newFiles(cfg *config.Config, db *postgres.DB, log *slog.Logger) (*file.Service, error) {
	secret := []byte(cfg.Storage.URLSecret.Value())

	if len(secret) == 0 {
		secret = make([]byte, _ephemeralSecretBytes)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("app: file url secret: %w", err)
		}

		if cfg.Storage.Driver == config.StorageLocal {
			log.Warn("FILE_URL_SECRET is unset: file URLs stop working after a restart")
		}
	}

	return file.New(postgres.NewFiles(db), newStorage(&cfg.Storage), file.Config{Secret: secret, TTL: cfg.Storage.URLTTL}, log, nil), nil
}
