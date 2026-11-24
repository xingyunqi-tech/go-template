package postgres

import (
	"context"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// QueueMail is the River queue of the mail jobs.
const QueueMail = "mail"

// MailArgs are the River arguments of a mail job.
type MailArgs struct {
	Mail   string    `json:"mail"`
	UserID uuid.UUID `json:"userId"`
	Locale string    `json:"locale"`
}

// Kind implements river.JobArgs.
func (MailArgs) Kind() string { return "mail" }

// InsertOpts implements river.JobArgsWithInsertOpts.
func (MailArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueMail} //nolint:exhaustruct // defaults
}

// MailQueue implements repo.MailQueue with an insert-only River client.
type MailQueue struct {
	client *river.Client[pgx.Tx]
}

// NewMailQueue returns MailQueue.
func NewMailQueue(client *river.Client[pgx.Tx]) *MailQueue {
	return &MailQueue{client: client}
}

var _ repo.MailQueue = (*MailQueue)(nil)

// Enqueue implements repo.MailQueue: in the request transaction, so the job exists only when the
// transaction commits.
func (q *MailQueue) Enqueue(ctx context.Context, job *repo.MailJob) error {
	_, err := insert(ctx, q.client, MailArgs{Mail: job.Kind, UserID: job.UserID, Locale: job.Locale}, nil)
	if err != nil {
		return fmt.Errorf("postgres: enqueue mail: %w", err)
	}

	return nil
}
