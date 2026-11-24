// Package account deletes accounts: deleteMe and deleteUser soft-delete the user and sign it out,
// the account.purge job removes its data and publishes user.deleted (platform conventions §10).
package account

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/events"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
)

// TxRunner runs fn in a transaction of its own.
type TxRunner interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Deps are what the Service uses. Purgers run in order inside the purge transaction.
type Deps struct {
	Users      repo.Users
	Identities repo.Identities
	Revocation *session.Revocation
	Queue      repo.AccountQueue
	Unpurged   repo.Maintenance
	Purgers    []repo.Purger
	Publisher  events.Publisher
	Tx         TxRunner
	Log        *slog.Logger
	// Grace is ACCOUNT_DELETION_GRACE, the delay before the purge.
	Grace time.Duration
	// Source is the CloudEvents source, OTEL_SERVICE_NAME.
	Source string
	Now    func() time.Time
}

// Service implements usecase.Accounts and runs the purge jobs.
type Service struct {
	Deps
}

var _ usecase.Accounts = (*Service)(nil)

// New returns a Service; a nil d.Now is time.Now.
func New(d *Deps) *Service {
	s := &Service{Deps: *d}
	if s.Now == nil {
		s.Now = time.Now
	}

	return s
}

// DeleteMe implements usecase.Accounts.
func (s *Service) DeleteMe(ctx context.Context, caller *usecase.Caller) error {
	return s.Delete(ctx, caller.UserID)
}

// Delete implements usecase.Accounts in the request transaction: the last active admin stays,
// the identities are removed at once so they can sign up again, and the purge is scheduled
// after the grace period.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	rec, found, err := s.Users.GetForUpdate(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if !found {
		return entity.New(entity.KeyUserNotFound)
	}

	if err := user.KeepAdmin(ctx, s.Users, &rec.User); err != nil {
		return err //nolint:wrapcheck // entity or repo errors
	}

	if _, err := s.Users.SoftDelete(ctx, id); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if err := s.Identities.DeleteAll(ctx, id); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if err := s.Revocation.All(ctx, id, nil); err != nil {
		return err //nolint:wrapcheck // entity or repo errors
	}

	_, err = s.Queue.EnqueuePurge(ctx, id, s.Now().Add(s.Grace))

	return err //nolint:wrapcheck // repo errors are wrapped
}

// Purge runs an account.purge job: the purgers and the removal of the user share one
// transaction, user.deleted is published after it commits. A user that is not soft-deleted is
// left alone; one already removed is purged again, which the purgers allow.
func (s *Service) Purge(ctx context.Context, id uuid.UUID) error {
	deleted, found, err := s.Users.Deleted(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if found && !deleted {
		s.Log.WarnContext(ctx, "account purge skipped: user is not deleted", "user_id", id)

		return nil
	}

	err = s.Tx.InTx(ctx, func(ctx context.Context) error {
		for _, p := range s.Purgers {
			if err := p.Purge(ctx, id); err != nil {
				return err //nolint:wrapcheck // repo errors are wrapped
			}
		}

		return s.Users.Purge(ctx, id) //nolint:wrapcheck // repo errors are wrapped
	})
	if err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	e, err := events.NewUserDeleted(s.Source, id, s.Now())
	if err != nil {
		return err //nolint:wrapcheck // wrapped by events
	}

	if err := s.Publisher.Publish(ctx, e); err != nil {
		return fmt.Errorf("account: publish: %w", err)
	}

	return nil
}

const (
	// _sweepMargin leaves a scheduled purge time to run before the sweep schedules it again.
	_sweepMargin = time.Hour
	_sweepBatch  = 500
)

// Sweep schedules the purge, now, of every user deleted longer than the grace period ago that is
// still stored, page by page; a purge already waiting for a user is kept.
func (s *Service) Sweep(ctx context.Context) error {
	now := s.Now()
	before := now.Add(-s.Grace - _sweepMargin)

	var (
		after       *repo.UnpurgedUser
		rescheduled int
	)

	for {
		page, err := s.Unpurged.UnpurgedUsers(ctx, before, after, _sweepBatch)
		if err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}

		for i := range page {
			added, err := s.Queue.EnqueuePurge(ctx, page[i].ID, now)
			if err != nil {
				return err //nolint:wrapcheck // repo errors are wrapped
			}

			if added {
				rescheduled++
			}
		}

		if len(page) < _sweepBatch {
			break
		}

		after = &page[len(page)-1]
	}

	if rescheduled > 0 {
		s.Log.WarnContext(ctx, "account purge rescheduled", "users", rescheduled)
	}

	return nil
}
