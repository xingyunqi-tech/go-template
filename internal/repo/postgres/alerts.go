package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const (
	_roleAdmin = 1

	_insertAlertSQL = `INSERT INTO "admin_alert" ("type", "level", "message", "payload", "dedupKey")
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT DO NOTHING
RETURNING "id"`

	_adminIDsSQL = `SELECT "id" FROM "user" WHERE "roleId" = $1 AND "deletedAt" IS NULL ORDER BY "id" LIMIT $2`

	_notifySQL = `INSERT INTO "notification" ("userId", "type", "category", "title", "content", "metadata", "channels", "sentChannels", "isRead")
SELECT $1::int, $2::varchar, $3::varchar, $4::varchar, $5::text, $6::jsonb,
  CASE WHEN s."enableInApp" = false THEN '[]'::jsonb ELSE '["IN_APP"]'::jsonb END,
  '{}'::jsonb, false
FROM (SELECT 1) AS one
LEFT JOIN "notification_setting" AS s ON s."userId" = $1::int AND s."category" = $3::varchar`
)

// WithoutTx returns ctx detached from the request transaction.
func WithoutTx(ctx context.Context) context.Context {
	return context.WithValue(ctx, txKey{}, (*txState)(nil))
}

// Alerts implements repo.Alerts.
type Alerts struct {
	db *DB
}

// NewAlerts returns Alerts.
func NewAlerts(db *DB) *Alerts {
	return &Alerts{db: db}
}

var _ repo.Alerts = (*Alerts)(nil)

// CreateIfNoOpen implements repo.Alerts.
func (a *Alerts) CreateIfNoOpen(ctx context.Context, alert *entity.Alert) (id int, created bool, err error) {
	var rowID int32

	err = a.db.dbtx(WithoutTx(ctx), func(db sqlcgen.DBTX) error {
		return db.QueryRow(ctx, _insertAlertSQL, alert.Type, string(alert.Level), alert.Message, alert.Payload, alert.DedupKey).Scan(&rowID)
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, false, nil
	case err != nil:
		return 0, false, fmt.Errorf("postgres: insert alert: %w", err)
	default:
		return int(rowID), true, nil
	}
}

// AdminIDs implements repo.Alerts.
func (a *Alerts) AdminIDs(ctx context.Context, limit int) ([]int, error) {
	var ids []int

	err := a.db.dbtx(WithoutTx(ctx), func(db sqlcgen.DBTX) error {
		rows, err := db.Query(ctx, _adminIDsSQL, _roleAdmin, limit)
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
		return nil, fmt.Errorf("postgres: admin ids: %w", err)
	}

	return ids, nil
}

// Notify implements repo.Alerts.
func (a *Alerts) Notify(ctx context.Context, n *entity.Notification) error {
	err := a.db.dbtx(WithoutTx(ctx), func(db sqlcgen.DBTX) error {
		_, err := db.Exec(ctx, _notifySQL, n.UserID, n.Type, n.Category, n.Title, n.Content, n.Metadata)

		return err
	})
	if err != nil {
		return fmt.Errorf("postgres: notify: %w", err)
	}

	return nil
}
