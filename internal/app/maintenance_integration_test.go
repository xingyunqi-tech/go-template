//go:build integration

package app_test

import (
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/internal/usecase/audit"
	"github.com/xingyunqi-tech/go-template/internal/usecase/maintenance"
)

func TestListAuditLogsPagesNewestFirst(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("ada@example.com")

	for _, title := range []string{"a", "b", "c"} {
		require.Equal(t, http.StatusCreated, a.do(http.MethodPost, "/api/v1/notes", s.access, `{"title":"`+title+`"}`).Status)
	}

	r := a.do(http.MethodGet, "/api/v1/audit-logs", s.access, "")
	require.Equal(t, http.StatusForbidden, r.Status, r.Raw)

	admin := testutil.IssueAccessToken(t, &testutil.TokenOptions{Roles: []string{"admin", "user"}})
	id, _ := s.user["id"].(string)

	r = a.do(http.MethodGet, "/api/v1/audit-logs?limit=2&action=note.create&actorSub="+id, admin, "")
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	items, _ := r.data()["items"].([]any)
	require.Len(t, items, 2)

	first, _ := items[0].(map[string]any)
	assert.Equal(t, "note.create", first["action"])
	assert.Equal(t, id, first["actorSub"])
	assert.Equal(t, "note", first["resourceType"])

	cursor, _ := r.data()["nextCursor"].(string)
	require.NotEmpty(t, cursor)

	r = a.do(http.MethodGet, "/api/v1/audit-logs?limit=2&action=note.create&cursor="+cursor, admin, "")
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	rest, _ := r.data()["items"].([]any)
	assert.Len(t, rest, 1)
	assert.Nil(t, r.data()["nextCursor"])

	r = a.do(http.MethodGet, "/api/v1/audit-logs?cursor=nope", admin, "")
	require.Equal(t, http.StatusUnprocessableEntity, r.Status, r.Raw)
	assert.Contains(t, r.Raw, `"field":"query.cursor"`)
}

func TestMaintenanceDeletesWhatExpired(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("ada@example.com")
	id, _ := s.user["id"].(string)

	a.exec(`UPDATE session SET expires_at = now() - interval '1 minute'`)
	a.exec(`INSERT INTO user_token (user_id, purpose, token_hash, expires_at) VALUES ('` + id + `', 'password_reset', '\x01', now() - interval '1 minute')`)
	a.exec(`INSERT INTO audit_log (actor_sub, action, resource_type, created_at) VALUES (NULL, 'old', 'note', now() - interval '200 days')`)
	a.exec(`INSERT INTO audit_log (actor_sub, action, resource_type) VALUES (NULL, 'recent', 'note')`)

	db := postgres.NewDB(a.infra.Pool, time.Second, slog.New(slog.DiscardHandler))

	require.NoError(t, maintenance.New(&maintenance.Deps{
		Identity: postgres.NewMaintenance(db), Audits: audit.New(postgres.NewAudits(db)), Retention: 180 * 24 * time.Hour,
		Log: slog.New(slog.DiscardHandler), Now: nil,
	}).Run(t.Context()))

	assert.Equal(t, 0, a.count(`SELECT count(*) FROM session`))
	assert.Equal(t, 0, a.count(`SELECT count(*) FROM user_token WHERE expires_at < now()`))
	assert.Equal(t, 0, a.count(`SELECT count(*) FROM audit_log WHERE action = 'old'`))
	assert.Equal(t, 1, a.count(`SELECT count(*) FROM audit_log WHERE action = 'recent'`))
	assert.Equal(t, http.StatusUnauthorized, a.refresh(s.refresh).Status)
}

func TestUnpurgedUsersAreTheOldDeletedOnes(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	old := a.register("old@example.com")
	recent := a.register("recent@example.com")
	a.register("live@example.com")

	oldID, _ := old.user["id"].(string)
	recentID, _ := recent.user["id"].(string)

	a.exec(`UPDATE app_user SET deleted_at = now() - interval '10 days' WHERE id = '` + oldID + `'`)
	a.exec(`UPDATE app_user SET deleted_at = now() WHERE id = '` + recentID + `'`)

	db := postgres.NewDB(a.infra.Pool, time.Second, slog.New(slog.DiscardHandler))

	m := postgres.NewMaintenance(db)

	users, err := m.UnpurgedUsers(t.Context(), time.Now().Add(-7*24*time.Hour), nil, 10)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, oldID, users[0].ID.String())

	rest, err := m.UnpurgedUsers(t.Context(), time.Now().Add(-7*24*time.Hour), &users[0], 10)
	require.NoError(t, err)
	assert.Empty(t, rest)
}

func TestAuditPruningDeletesInBatches(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	a.exec(`INSERT INTO audit_log (action, resource_type, created_at)
		SELECT 'old', 'note', now() - interval '1 year' FROM generate_series(1, 25000)`)

	db := postgres.NewDB(a.infra.Pool, time.Second, slog.New(slog.DiscardHandler))

	n, err := postgres.NewAudits(db).DeleteBefore(t.Context(), time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(25000), n)
	assert.Equal(t, 0, a.count(`SELECT count(*) FROM audit_log WHERE action = 'old'`))
}

func TestTheWorkerRunsThePeriodicJobsOnStart(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)

	require.Eventually(t, func() bool {
		return a.count(`SELECT count(*) FROM river_job WHERE kind IN ('maintenance', 'account.sweep') AND state = 'completed'`) == 2
	}, 10*time.Second, 50*time.Millisecond)
}
