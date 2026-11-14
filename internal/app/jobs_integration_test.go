//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	"github.com/xingyunqi-tech/go-template/internal/worker"
)

const _waitFor = 15 * time.Second

type captured struct {
	mu   sync.Mutex
	sent []mail.Outgoing
}

func (c *captured) Send(_ context.Context, m *mail.Outgoing) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sent = append(c.sent, *m)

	return nil
}

func (c *captured) all() []mail.Outgoing {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]mail.Outgoing(nil), c.sent...)
}

func withFrontend(c *config.Config) {
	c.App.FrontendDomain = "http://localhost:3000"
}

func startWorker(t *testing.T, a *testApp, sender mail.Sender) *river.Client[pgx.Tx] {
	t.Helper()

	infra, err := app.OpenInfra(t.Context(), a.cfg, 5)
	require.NoError(t, err)
	t.Cleanup(infra.Close)

	client, err := app.NewWorkerClient(a.cfg, slog.New(slog.DiscardHandler), infra, sender)
	require.NoError(t, err)
	require.NoError(t, client.Start(context.WithoutCancel(t.Context())))

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_ = client.Stop(ctx)
	})

	return client
}

type jobRow struct {
	Kind, Queue, State string
	MaxAttempts        int
	Args               map[string]any
}

func jobs(t *testing.T, a *testApp, kind string) []jobRow {
	t.Helper()

	rows, err := a.pool.Query(t.Context(),
		`SELECT kind, queue, state::text, max_attempts, args FROM river.river_job WHERE kind = $1 ORDER BY id`, kind)
	require.NoError(t, err)

	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (jobRow, error) {
		var (
			r   jobRow
			raw []byte
		)

		err := row.Scan(&r.Kind, &r.Queue, &r.State, &r.MaxAttempts, &raw)
		if err == nil {
			err = json.Unmarshal(raw, &r.Args)
		}

		return r, err
	})
	require.NoError(t, err)

	return out
}

func TestRegistrationQueuesMailInTheRequestLanguage(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	r := a.do(http.MethodPost, "/api/v1/auth/email/register", "", map[string]any{
		"email": "queued@example.com", "password": _secret00, "firstName": "Q", "lastName": "M",
	}, "Accept-Language", "zh")
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	queued := jobs(t, a, "send-mail")
	require.Len(t, queued, 1)
	assert.Equal(t, "mail", queued[0].Queue)
	assert.Equal(t, 3, queued[0].MaxAttempts)
	assert.Equal(t, "activation", queued[0].Args["template"])
	assert.Equal(t, "queued@example.com", queued[0].Args["to"])
	assert.Equal(t, "zh", queued[0].Args["language"])
	data, ok := queued[0].Args["data"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, data["hash"])

	r = a.do(http.MethodPost, "/api/v1/auth/email/register", "", map[string]any{
		"email": "queued@example.com", "password": _secret00, "firstName": "Q", "lastName": "M",
	})
	assert.Equal(t, http.StatusUnprocessableEntity, r.Status)
	assert.Len(t, jobs(t, a, "send-mail"), 1)
}

func TestWorkerSendsQueuedMail(t *testing.T) {
	t.Parallel()

	a := newAppWith(t, 0, withFrontend, nil)
	sender := &captured{}
	startWorker(t, a, sender)

	r := a.do(http.MethodPost, "/api/v1/auth/forgot/password", "", map[string]any{"email": "john.doe@example.com"}, "Accept-Language", "zh")
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	require.Eventually(t, func() bool { return len(sender.all()) == 1 }, _waitFor, 50*time.Millisecond)

	got := sender.all()[0]
	assert.Equal(t, "john.doe@example.com", got.To)
	assert.Equal(t, "重置密码", got.Subject)
	assert.Contains(t, got.Text, "http://localhost:3000/password-change?hash=")
	assert.Contains(t, got.Text, "&expires=")

	require.Eventually(t, func() bool {
		q := jobs(t, a, "send-mail")

		return len(q) == 1 && q[0].State == "completed"
	}, _waitFor, 50*time.Millisecond)
}

func TestDeletedAccountIsCleanedOnce(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	var userID int
	require.NoError(t, a.pool.QueryRow(t.Context(), `WITH f AS (INSERT INTO "file" ("path") VALUES ('avatar.png') RETURNING "id")
		UPDATE "user" SET "photoId" = (SELECT "id" FROM f) WHERE "email" = 'john.doe@example.com' RETURNING "id"`).Scan(&userID))

	login := a.login("john.doe@example.com", _secret00)
	r := a.do(http.MethodDelete, "/api/v1/auth/me", login.access, map[string]any{"oldPassword": _secret00})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	queued := jobs(t, a, "clean-account")
	require.Len(t, queued, 1)
	assert.Equal(t, "account-cleanup", queued[0].Queue)
	assert.Equal(t, 5, queued[0].MaxAttempts)
	assert.InDelta(t, userID, queued[0].Args["userId"], 0)

	inserter, err := river.NewClient(riverpgxv5.New(a.pool), postgres.RiverConfig(slog.New(slog.DiscardHandler)))
	require.NoError(t, err)
	require.NoError(t, postgres.NewJobs(inserter).EnqueueAccountCleanup(t.Context(), userID))
	assert.Len(t, jobs(t, a, "clean-account"), 1)

	startWorker(t, a, &captured{})

	require.Eventually(t, func() bool {
		var done bool

		_ = a.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM "account_cleanup" WHERE "userId" = $1)`, userID).Scan(&done)

		return done
	}, _waitFor, 50*time.Millisecond)

	var photo *string
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "photoId"::text FROM "user" WHERE "id" = $1`, userID).Scan(&photo))
	assert.Nil(t, photo)

	require.Eventually(t, func() bool { return jobs(t, a, "clean-account")[0].State == "completed" }, _waitFor, 50*time.Millisecond)
	require.NoError(t, postgres.NewJobs(inserter).EnqueueAccountCleanup(t.Context(), userID))
	assert.Len(t, jobs(t, a, "clean-account"), 2)
}

func TestPeriodicTasks(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)

	var johnID, adminID int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "id" FROM "user" WHERE "email" = 'john.doe@example.com'`).Scan(&johnID))
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "id" FROM "user" WHERE "email" = 'admin@example.com'`).Scan(&adminID))

	_, err := a.pool.Exec(t.Context(), `INSERT INTO "session" ("hash", "userId", "deletedAt") VALUES
		('old', $1, NOW() - INTERVAL '8 days'), ('recent', $1, NOW() - INTERVAL '6 days'), ('live', $1, NULL)`, johnID)
	require.NoError(t, err)

	_, err = a.pool.Exec(t.Context(), `INSERT INTO "notification" ("userId", "type", "category", "title", "content", "isRead", "readAt") VALUES
		($1, 'INFO', 'SYSTEM', 'old', 'c', true, NOW() - INTERVAL '31 days'),
		($1, 'INFO', 'SYSTEM', 'recent', 'c', true, NOW() - INTERVAL '29 days'),
		($1, 'INFO', 'SYSTEM', 'unread', 'c', false, NULL)`, johnID)
	require.NoError(t, err)

	_, err = a.pool.Exec(t.Context(), `UPDATE "user" SET "deletedAt" = NOW() - INTERVAL '1 hour' WHERE "id" = $1`, adminID)
	require.NoError(t, err)

	client := startWorker(t, a, &captured{})

	for _, args := range []river.JobArgs{worker.SessionPurgeArgs{}, worker.ReadNotificationsArgs{}, worker.CompensationArgs{}} {
		_, err := client.Insert(t.Context(), args, &river.InsertOpts{Queue: "scheduler"})
		require.NoError(t, err)
	}

	require.Eventually(t, func() bool {
		var done bool

		_ = a.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM "account_cleanup" WHERE "userId" = $1)`, adminID).Scan(&done)

		return done
	}, _waitFor, 50*time.Millisecond)

	var sessions []string

	rows, err := a.pool.Query(t.Context(), `SELECT "hash" FROM "session" WHERE "hash" IN ('old', 'recent', 'live') ORDER BY "hash"`)
	require.NoError(t, err)
	sessions, err = pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.Equal(t, []string{"live", "recent"}, sessions)

	rows, err = a.pool.Query(t.Context(), `SELECT "title" FROM "notification" WHERE "deletedAt" IS NOT NULL`)
	require.NoError(t, err)
	deleted, err := pgx.CollectRows(rows, pgx.RowTo[string])
	require.NoError(t, err)
	assert.Equal(t, []string{"old"}, deleted)
}

func TestFailedCleanupEnqueueStillDeletesAndAlerts(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	login := a.login("john.doe@example.com", _secret00)

	var id int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "id" FROM "user" WHERE "email" = 'john.doe@example.com'`).Scan(&id))

	_, err := a.pool.Exec(t.Context(), `ALTER TABLE river.river_job RENAME TO river_job_unavailable`)
	require.NoError(t, err)

	r := a.do(http.MethodDelete, "/api/v1/auth/me", login.access, map[string]any{"oldPassword": _secret00})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	var deleted bool
	require.NoError(t, a.pool.QueryRow(t.Context(),
		`SELECT "deletedAt" IS NOT NULL FROM "user" WHERE "id" = $1`, id).Scan(&deleted))
	assert.True(t, deleted)

	var alertType string
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "type" FROM "admin_alert"`).Scan(&alertType))
	assert.Equal(t, "accountCleanupFailed", alertType)
}
