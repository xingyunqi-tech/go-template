//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
)

func TestAlertsAreDeduplicatedWhileOpen(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)
	alerts := postgres.NewAlerts(db)
	a := &entity.Alert{Type: "t", Level: entity.AlertWarn, Message: "m", Payload: map[string]any{"k": 1}, DedupKey: "t:1"}

	id, created, err := alerts.CreateIfNoOpen(t.Context(), a)
	require.NoError(t, err)
	assert.True(t, created)

	_, created, err = alerts.CreateIfNoOpen(t.Context(), a)
	require.NoError(t, err)
	assert.False(t, created)

	_, err = pool.Exec(t.Context(), `UPDATE "admin_alert" SET "acknowledgedAt" = now() WHERE "id" = $1`, id)
	require.NoError(t, err)

	_, created, err = alerts.CreateIfNoOpen(t.Context(), a)
	require.NoError(t, err)
	assert.True(t, created)

	admins, err := alerts.AdminIDs(t.Context(), 50)
	require.NoError(t, err)
	require.Len(t, admins, 1)

	_, err = pool.Exec(t.Context(), `INSERT INTO "notification_setting" ("userId", "category", "enableInApp") VALUES ($1, 'SYSTEM', false)`, admins[0])
	require.NoError(t, err)
	require.NoError(t, alerts.Notify(t.Context(), &entity.Notification{
		UserID: admins[0], Type: "WARNING", Category: "SYSTEM", Title: "x", Content: "c", Metadata: map[string]any{"alertId": id},
	}))

	var channels string
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT "channels"::text FROM "notification"`).Scan(&channels))
	assert.Equal(t, "[]", channels)
}

func TestAlertsAreWrittenOutsideTheRequestTransaction(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)

	ctx, tx, err := db.Begin(t.Context())
	require.NoError(t, err)

	_, _, err = postgres.NewAlerts(db).CreateIfNoOpen(ctx, &entity.Alert{Type: "t", Level: entity.AlertInfo, Message: "m", Payload: nil, DedupKey: "t"})
	require.NoError(t, err)
	require.NoError(t, postgres.NewAudits(db).Create(ctx, &repo.AuditLog{Action: "UPDATE", EntityType: "User", EntityID: "1"}))
	tx.Rollback(ctx)

	var alerts, audits int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM "admin_alert"`).Scan(&alerts))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM "audit_logs"`).Scan(&audits))
	assert.Equal(t, []int{1, 1}, []int{alerts, audits})
}

func TestAccountCleanupsAndMaintenance(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)
	cleanups := postgres.NewAccountCleanups(db)

	var removed, fresh int
	require.NoError(t, pool.QueryRow(t.Context(),
		`UPDATE "user" SET "deletedAt" = now() - interval '2 hours' WHERE "email" = 'john.doe@example.com' RETURNING "id"`).Scan(&removed))
	require.NoError(t, pool.QueryRow(t.Context(),
		`UPDATE "user" SET "deletedAt" = now() WHERE "email" = 'admin@example.com' RETURNING "id"`).Scan(&fresh))

	ids, err := cleanups.PendingUserIDs(t.Context(), time.Now().Add(-30*time.Minute), 200)
	require.NoError(t, err)
	assert.Equal(t, []int{removed}, ids)

	require.NoError(t, cleanups.MarkCompleted(t.Context(), removed))
	require.NoError(t, cleanups.MarkCompleted(t.Context(), removed))

	ids, err = cleanups.PendingUserIDs(t.Context(), time.Now().Add(-30*time.Minute), 200)
	require.NoError(t, err)
	assert.Empty(t, ids)

	n, err := postgres.NewMaintenance(db).PurgeDeletedSessions(t.Context(), 7)
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestInTxRollsBackOnError(t *testing.T) {
	t.Parallel()

	db, pool := newDB(t)
	boom := errors.New("boom")

	err := db.InTx(t.Context(), func(ctx context.Context) error {
		_, err := postgres.NewFiles(db).Create(ctx, &entity.File{Path: "x", Visibility: entity.VisibilityPublic})
		require.NoError(t, err)

		return boom
	})
	require.ErrorIs(t, err, boom)

	var n int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM "file"`).Scan(&n))
	assert.Zero(t, n)
}
