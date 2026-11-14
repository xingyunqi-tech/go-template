package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const (
	_purgeSessionsSQL = `DELETE FROM "session"
WHERE "deletedAt" IS NOT NULL
  AND "deletedAt" < NOW() - make_interval(days => $1)`

	_cleanupNotificationsSQL = `UPDATE "notification"
SET "deletedAt" = NOW()
WHERE "isRead" = true
  AND "readAt" IS NOT NULL
  AND "readAt" < NOW() - make_interval(days => $1)
  AND "deletedAt" IS NULL`
)

// Maintenance implements repo.Maintenance.
type Maintenance struct {
	db *DB
}

// NewMaintenance returns Maintenance.
func NewMaintenance(db *DB) *Maintenance {
	return &Maintenance{db: db}
}

var _ repo.Maintenance = (*Maintenance)(nil)

func (m *Maintenance) exec(ctx context.Context, sql string, days int) (int64, error) {
	var affected int64

	err := m.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		tag, err := db.Exec(ctx, sql, days)
		affected = tag.RowsAffected()

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("postgres: maintenance: %w", err)
	}

	return affected, nil
}

// PurgeDeletedSessions implements repo.Maintenance.
func (m *Maintenance) PurgeDeletedSessions(ctx context.Context, retentionDays int) (int64, error) {
	return m.exec(ctx, _purgeSessionsSQL, retentionDays)
}

// SoftDeleteReadNotifications implements repo.Maintenance.
func (m *Maintenance) SoftDeleteReadNotifications(ctx context.Context, retentionDays int) (int64, error) {
	return m.exec(ctx, _cleanupNotificationsSQL, retentionDays)
}
