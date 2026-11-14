package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/i18n"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/storage"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
)

// newInserter returns a River client that only inserts jobs.
func newInserter(pool *pgxpool.Pool, log *slog.Logger) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), postgres.RiverConfig(log))
	if err != nil {
		return nil, fmt.Errorf("app: river: %w", err)
	}

	return client, nil
}

// queueMailer queues auth mails with the request language.
type queueMailer struct {
	jobs repo.Jobs
}

func (m queueMailer) Send(ctx context.Context, msg auth.Mail) error {
	return m.jobs.EnqueueMail(ctx, entity.MailJob{ //nolint:wrapcheck // repo errors are wrapped
		Template: msg.Template, To: msg.To, Data: msg.Data, Language: i18n.Language(ctx),
	})
}

// newSender sends over SMTP when MAIL_HOST is set, else only logs.
func newSender(cfg *config.Mail, log *slog.Logger) mail.Sender {
	if cfg.Host == "" {
		return mail.NewLog(log)
	}

	return mail.NewSMTP(&mail.SMTPConfig{
		Host: cfg.Host, Port: cfg.Port, User: cfg.User, Password: cfg.Password.Value(),
		DefaultEmail: cfg.DefaultEmail, DefaultName: cfg.DefaultName,
		Secure: cfg.Secure.Or(false), IgnoreTLS: cfg.IgnoreTLS.Or(false), RequireTLS: cfg.RequireTLS.Or(false),
	})
}

// newFiles builds the file service on the FILE_DRIVER storage.
func newFiles(cfg *config.Config, db *postgres.DB, log *slog.Logger) *file.Service {
	var store repo.FileStorage = storage.NewLocal(cfg.File.LocalRoot)

	if cfg.File.Driver != config.FileDriverLocal {
		store = storage.NewS3(&storage.S3Config{
			Bucket: cfg.File.Bucket, Region: cfg.File.Region, Endpoint: cfg.File.Endpoint,
			ForcePathStyle: cfg.File.PathStyleDisabled.On(),
			AccessKeyID:    cfg.File.AccessKeyID, SecretAccessKey: cfg.File.SecretAccessKey.Value(),
		})
	}

	return file.New(file.Config{
		APIPrefix: cfg.App.APIPrefix, BackendDomain: cfg.App.BackendDomain,
		MaxSizes: map[file.Purpose]int64{
			file.PurposeImage: cfg.File.ImageMaxSize, file.PurposePrivateImage: cfg.File.PrivateImageMax,
			file.PurposeContentImage: cfg.File.ContentImageMax, file.PurposeAudio: cfg.File.AudioMaxSize,
			file.PurposeImport: cfg.File.ImportMaxSize, file.PurposeExport: cfg.File.ExportMaxSize,
		},
	}, postgres.NewFiles(db), store, db, log)
}
