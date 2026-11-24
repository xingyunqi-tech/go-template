//go:build integration

package app_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

func immediatePurge(c *config.Config) {
	c.Account.DeletionGrace = 0
}

func (a *identityApp) count(sql string, args ...any) int {
	a.t.Helper()

	var n int

	require.NoError(a.t, a.infra.Pool.QueryRow(a.t.Context(), sql, args...).Scan(&n))

	return n
}

// waitPurged waits for the purge job to remove the user row.
func (a *identityApp) waitPurged(id string) {
	a.t.Helper()

	require.Eventually(a.t, func() bool {
		return a.count(`SELECT count(*) FROM app_user WHERE id = $1`, id) == 0
	}, 10*time.Second, 50*time.Millisecond)
}

func TestDeleteMeNeedsARecentSignIn(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("ada@example.com")
	sub, _ := s.user["id"].(string)
	id := uuid.MustParse(sub)

	stale := testutil.IssueAccessToken(t, &testutil.TokenOptions{Subject: id, Roles: []string{"user"}, AuthTime: time.Now().Add(-11 * time.Minute)})

	r := a.do(http.MethodDelete, "/api/v1/me", stale, "")
	require.Equal(t, http.StatusForbidden, r.Status, r.Raw)
	assert.Equal(t, float64(40303), r.Body["code"])
}

func TestDeleteMeSignsOutFreesTheEmailAndPurgesTheData(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false, immediatePurge)
	s := a.register("ada@example.com")
	id, _ := s.user["id"].(string)

	note := a.do(http.MethodPost, "/api/v1/notes", s.access, `{"title":"t","content":"c"}`)
	require.Equal(t, http.StatusCreated, note.Status, note.Raw)

	r := a.do(http.MethodDelete, "/api/v1/me", s.access, "")
	require.Equal(t, http.StatusAccepted, r.Status, r.Raw)
	assert.Nil(t, r.Body["data"])

	assert.Equal(t, http.StatusUnauthorized, a.get("/api/v1/me", s.access).Status)
	assert.Equal(t, http.StatusUnauthorized, a.refresh(s.refresh).Status)
	assert.Equal(t, http.StatusUnauthorized, a.login("ada@example.com", _password).Status)

	again := a.register("ada@example.com")
	assert.NotEqual(t, id, again.user["id"])

	a.waitPurged(id)

	assert.Equal(t, 0, a.count(`SELECT count(*) FROM note WHERE owner_sub = $1`, id))
	assert.Equal(t, 0, a.count(`SELECT count(*) FROM audit_log WHERE actor_sub = $1`, id))
	assert.Equal(t, 2, a.count(`SELECT count(*) FROM audit_log WHERE actor_sub IS NULL AND action IN ('note.create', 'user.delete')`))
	assert.Equal(t, 0, a.count(`SELECT count(*) FROM session WHERE user_id = $1`, id))

	require.Eventually(t, func() bool { return len(a.workerLog.Lines(`"type":"user.deleted"`)) == 1 }, 5*time.Second, 50*time.Millisecond)

	var line struct {
		Event string `json:"event"`
	}

	require.NoError(t, json.Unmarshal([]byte(a.workerLog.Lines(`"type":"user.deleted"`)[0]), &line))

	var event map[string]any

	require.NoError(t, json.Unmarshal([]byte(line.Event), &event))
	assert.Equal(t, "1.0", event["specversion"])
	assert.Equal(t, a.cfg.Telemetry.ServiceName, event["source"])
	assert.Equal(t, map[string]any{"userId": id}, event["data"])
}

func TestDeleteSchedulesThePurgeAfterTheGracePeriod(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("ada@example.com")
	id, _ := s.user["id"].(string)

	require.Equal(t, http.StatusAccepted, a.do(http.MethodDelete, "/api/v1/me", s.access, "").Status)

	var scheduled time.Time

	require.NoError(t, a.infra.Pool.QueryRow(t.Context(),
		`SELECT scheduled_at FROM river_job WHERE kind = 'account.purge' AND args->>'userId' = $1`, id).Scan(&scheduled))
	assert.WithinDuration(t, time.Now().Add(168*time.Hour), scheduled, time.Minute)
	assert.Equal(t, 1, a.count(`SELECT count(*) FROM app_user WHERE id = $1 AND deleted_at IS NOT NULL`, id))
}

func TestDeletedProviderIdentitySignsUpAgain(t *testing.T) {
	t.Parallel()

	a := newProviderApp(t)

	first := a.providerSignIn("wechat_miniapp", "om1.u1")
	require.Equal(t, http.StatusAccepted, a.do(http.MethodDelete, "/api/v1/me", first.access, "").Status)

	again := a.providerSignIn("wechat_app", "oa1.u1")
	assert.NotEqual(t, first.user["id"], again.user["id"])
}

func TestDeleteUser(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	admin := adminToken(t)
	ada := a.register("ada@example.com")
	id, _ := ada.user["id"].(string)

	r := a.do(http.MethodDelete, "/api/v1/users/"+id, ada.access, "")
	require.Equal(t, http.StatusForbidden, r.Status, r.Raw)

	r = a.do(http.MethodDelete, "/api/v1/users/"+id, admin, "")
	require.Equal(t, http.StatusAccepted, r.Status, r.Raw)
	assert.Equal(t, http.StatusUnauthorized, a.get("/api/v1/me", ada.access).Status)

	r = a.do(http.MethodDelete, "/api/v1/users/"+id, admin, "")
	require.Equal(t, http.StatusNotFound, r.Status, r.Raw)
	assert.Equal(t, float64(40403), r.Body["code"])

	r = a.get("/api/v1/users/"+id, admin)
	require.Equal(t, http.StatusNotFound, r.Status, r.Raw)

	assert.Equal(t, 1, a.count(`SELECT count(*) FROM audit_log WHERE action = 'user.delete' AND resource_id = $1`, id))
}

func TestTheLastAdminCannotBeDeleted(t *testing.T) {
	t.Parallel()

	a := newIdentityApp(t, false)
	s := a.register("root@example.com")
	id, _ := s.user["id"].(string)

	r := a.do(http.MethodPut, "/api/v1/users/"+id+"/roles", adminToken(t), `{"roles":["admin","user"]}`)
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	root := a.signIn("root@example.com")

	r = a.do(http.MethodDelete, "/api/v1/me", root.access, "")
	require.Equal(t, http.StatusConflict, r.Status, r.Raw)
	assert.Equal(t, float64(40904), r.Body["code"])

	r = a.do(http.MethodDelete, "/api/v1/users/"+id, adminToken(t), "")
	require.Equal(t, http.StatusConflict, r.Status, r.Raw)
}
