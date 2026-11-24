package postgres

import (
	"context"
	"fmt"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// QueueAccount is the River queue of the account purge jobs.
const QueueAccount = "account"

// PurgeArgs are the River arguments of an account.purge job.
type PurgeArgs struct {
	UserID uuid.UUID `json:"userId"`
}

// Kind implements river.JobArgs.
func (PurgeArgs) Kind() string { return "account.purge" }

// InsertOpts implements river.JobArgsWithInsertOpts: one job per user.
func (PurgeArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: QueueAccount, UniqueOpts: river.UniqueOpts{ByArgs: true}} //nolint:exhaustruct // defaults
}

// AccountQueue implements repo.AccountQueue with an insert-only River client.
type AccountQueue struct {
	client *river.Client[pgx.Tx]
}

// NewAccountQueue returns AccountQueue.
func NewAccountQueue(client *river.Client[pgx.Tx]) *AccountQueue {
	return &AccountQueue{client: client}
}

var _ repo.AccountQueue = (*AccountQueue)(nil)

// EnqueuePurge implements repo.AccountQueue.
func (q *AccountQueue) EnqueuePurge(ctx context.Context, userID uuid.UUID, at time.Time) (bool, error) {
	opts := &river.InsertOpts{ScheduledAt: at} //nolint:exhaustruct // the rest comes from PurgeArgs

	res, err := insert(ctx, q.client, PurgeArgs{UserID: userID}, opts)
	if err != nil {
		return false, fmt.Errorf("postgres: enqueue purge: %w", err)
	}

	return !res.UniqueSkippedAsDuplicate, nil
}

// insert adds a job in the request transaction, so it exists only when the transaction commits.
func insert(ctx context.Context, client *river.Client[pgx.Tx], args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	s := stateOf(ctx)
	if s == nil {
		return client.Insert(ctx, args, opts) //nolint:wrapcheck // wrapped by the caller
	}

	tx, err := s.open(ctx)
	if err != nil {
		return nil, err
	}

	return client.InsertTx(ctx, tx, args, opts) //nolint:wrapcheck // wrapped by the caller
}
