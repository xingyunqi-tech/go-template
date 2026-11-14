package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const (
	_markCleanedSQL = `INSERT INTO "account_cleanup" ("userId", "completedAt") VALUES ($1, now())
ON CONFLICT ("userId") DO UPDATE SET "completedAt" = EXCLUDED."completedAt"`

	_pendingCleanupSQL = `SELECT u."id" FROM "user" AS u
WHERE u."deletedAt" IS NOT NULL
  AND u."deletedAt" < (CAST($1 AS timestamptz) AT TIME ZONE current_setting('TimeZone'))
  AND NOT EXISTS (SELECT 1 FROM "account_cleanup" AS c WHERE c."userId" = u."id")
ORDER BY u."id"
LIMIT $2`

	_detachPhotoSQL = `UPDATE "user" SET "photoId" = NULL, "updatedAt" = CURRENT_TIMESTAMP WHERE "id" = $1`

	_userStateSQL = `SELECT "deletedAt" IS NOT NULL FROM "user" WHERE "id" = $1`
)

// AccountCleanups implements repo.AccountCleanups.
type AccountCleanups struct {
	db *DB
}

// NewAccountCleanups returns AccountCleanups.
func NewAccountCleanups(db *DB) *AccountCleanups {
	return &AccountCleanups{db: db}
}

var _ repo.AccountCleanups = (*AccountCleanups)(nil)

func (a *AccountCleanups) exec(ctx context.Context, name, sql string, args ...any) error {
	err := a.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		_, err := db.Exec(ctx, sql, args...)

		return err
	})
	if err != nil {
		return fmt.Errorf("postgres: %s: %w", name, err)
	}

	return nil
}

// MarkCompleted implements repo.AccountCleanups.
func (a *AccountCleanups) MarkCompleted(ctx context.Context, userID int) error {
	return a.exec(ctx, "mark cleanup completed", _markCleanedSQL, userID)
}

// DetachPhoto implements repo.AccountCleanups.
func (a *AccountCleanups) DetachPhoto(ctx context.Context, userID int) error {
	return a.exec(ctx, "detach photo", _detachPhotoSQL, userID)
}

// PendingUserIDs implements repo.AccountCleanups.
func (a *AccountCleanups) PendingUserIDs(ctx context.Context, deletedBefore time.Time, limit int) ([]int, error) {
	var ids []int

	err := a.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		rows, err := db.Query(ctx, _pendingCleanupSQL, deletedBefore, limit)
		if err != nil {
			return err
		}

		ids, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (int, error) {
			var id int32

			err := row.Scan(&id)

			return int(id), err
		})

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: pending cleanups: %w", err)
	}

	return ids, nil
}

// UserState implements repo.AccountCleanups.
func (a *AccountCleanups) UserState(ctx context.Context, userID int) (exists, removed bool, err error) {
	err = a.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		return db.QueryRow(ctx, _userStateSQL, userID).Scan(&removed)
	})

	switch {
	case notFound(err):
		return false, false, nil
	case err != nil:
		return false, false, fmt.Errorf("postgres: user state: %w", err)
	default:
		return true, removed, nil
	}
}
