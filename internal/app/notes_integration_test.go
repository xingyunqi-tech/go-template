//go:build integration

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/testutil"
	"github.com/xingyunqi-tech/go-template/openapi"
)

// specRouter finds the documented operation of a response to validate it against openapi.json.
func specRouter(t *testing.T) routers.Router {
	t.Helper()

	doc, err := openapi3.NewLoader().LoadFromData(openapi.Document)
	require.NoError(t, err)

	r, err := legacy.NewRouter(doc)
	require.NoError(t, err)

	return r
}

// do sends a request and, for /api/v1 routes, checks the response against the document.
func (a *testApp) do(method, path, token, body string, headers ...string) *result {
	a.t.Helper()

	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(a.t.Context(), method, a.url+path, reader)
	require.NoError(a.t, err)

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(a.t, err)

	out := &result{Status: res.StatusCode, Header: res.Header, Raw: string(raw), Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)

	if strings.HasPrefix(path, "/api/v1/") {
		a.checkResponse(method, path, body, out)
	}

	return out
}

func (a *testApp) checkResponse(method, path, body string, out *result) {
	a.t.Helper()

	req, err := http.NewRequestWithContext(a.t.Context(), method, "http://localhost"+path, strings.NewReader(body))
	require.NoError(a.t, err)

	route, params, err := specRouter(a.t).FindRoute(req)
	if err != nil {
		return
	}

	input := &openapi3filter.ResponseValidationInput{ //nolint:exhaustruct // defaults
		RequestValidationInput: &openapi3filter.RequestValidationInput{ //nolint:exhaustruct // defaults
			Request: req, PathParams: params, Route: route,
		},
		Status: out.Status,
		Header: out.Header,
		Body:   io.NopCloser(bytes.NewReader([]byte(out.Raw))),
	}

	err = openapi3filter.ValidateResponse(a.t.Context(), input)
	assert.NoError(a.t, err, "%s %s %d: %s", method, path, out.Status, out.Raw)
}

func (r *result) data() map[string]any {
	d, _ := r.Body["data"].(map[string]any)

	return d
}

func notesApp(t *testing.T) *testApp {
	t.Helper()

	redisDB := _redis.Borrow(t)

	return newApp(t, func(c *config.Config) { c.Redis.URL = config.Secret(redisDB.URL()) }, nil)
}

func TestNotesLifecycle(t *testing.T) {
	t.Parallel()

	a := notesApp(t)
	user := uuid.NewV7()
	token := testutil.IssueAccessToken(t, &testutil.TokenOptions{Subject: user})

	created := a.do(http.MethodPost, "/api/v1/notes", token, `{"title":"first","content":"body"}`)
	require.Equal(t, http.StatusCreated, created.Status, created.Raw)
	assert.InDelta(t, 0, created.Body["code"], 0)
	assert.Equal(t, "ok", created.Body["message"])

	id, ok := created.data()["id"].(string)
	require.True(t, ok)

	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	assert.Equal(t, byte(7), parsed[6]>>4)
	assert.Regexp(t, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`, created.data()["createdAt"])

	got := a.do(http.MethodGet, "/api/v1/notes/"+id, token, "")
	require.Equal(t, http.StatusOK, got.Status)
	assert.Equal(t, "body", got.data()["content"])

	updated := a.do(http.MethodPatch, "/api/v1/notes/"+id, token, `{"title":"renamed"}`)
	require.Equal(t, http.StatusOK, updated.Status, updated.Raw)
	assert.Equal(t, "renamed", updated.data()["title"])
	assert.Equal(t, "body", updated.data()["content"])

	list := a.do(http.MethodGet, "/api/v1/notes", token, "")
	require.Equal(t, http.StatusOK, list.Status)
	assert.Len(t, list.data()["items"], 1)
	assert.Nil(t, list.data()["nextCursor"])

	deleted := a.do(http.MethodDelete, "/api/v1/notes/"+id, token, "")
	require.Equal(t, http.StatusOK, deleted.Status)
	assert.JSONEq(t, `{"code":0,"message":"ok","data":null}`, deleted.Raw)

	gone := a.do(http.MethodGet, "/api/v1/notes/"+id, token, "")
	assert.Equal(t, http.StatusNotFound, gone.Status)
	assert.Equal(t, "common.notFound", gone.Body["errorKey"])

	rows, err := a.infra.Pool.Query(t.Context(), `SELECT action, resource_id, actor_sub FROM audit_log ORDER BY created_at`)
	require.NoError(t, err)

	var actions []string

	for rows.Next() {
		var (
			action, resource string
			actor            uuid.UUID
		)

		require.NoError(t, rows.Scan(&action, &resource, &actor))
		assert.Equal(t, id, resource)
		assert.Equal(t, user, actor)

		actions = append(actions, action)
	}

	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"note.create", "note.update", "note.delete"}, actions)
}

func TestNotesAreVisibleToTheirOwnerOnly(t *testing.T) {
	t.Parallel()

	a := notesApp(t)
	alice := testutil.IssueAccessToken(t, nil)
	bob := testutil.IssueAccessToken(t, nil)

	created := a.do(http.MethodPost, "/api/v1/notes", alice, `{"title":"private"}`)
	require.Equal(t, http.StatusCreated, created.Status)

	id, _ := created.data()["id"].(string)

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		assert.Equal(t, http.StatusNotFound, a.do(method, "/api/v1/notes/"+id, bob, "").Status, method)
	}

	assert.Equal(t, http.StatusNotFound, a.do(http.MethodPatch, "/api/v1/notes/"+id, bob, `{"title":"x"}`).Status)
	assert.Empty(t, a.do(http.MethodGet, "/api/v1/notes", bob, "").data()["items"])
}

func TestNotesCursorPaging(t *testing.T) {
	t.Parallel()

	a := notesApp(t)
	token := testutil.IssueAccessToken(t, nil)

	for _, title := range []string{"n1", "n2", "n3", "n4", "n5"} {
		require.Equal(t, http.StatusCreated, a.do(http.MethodPost, "/api/v1/notes", token, `{"title":"`+title+`"}`).Status)
	}

	var (
		titles []string
		cursor string
	)

	for range 4 {
		path := "/api/v1/notes?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}

		page := a.do(http.MethodGet, path, token, "")
		require.Equal(t, http.StatusOK, page.Status, page.Raw)

		items, _ := page.data()["items"].([]any)
		for _, item := range items {
			n, _ := item.(map[string]any)
			titles = append(titles, n["title"].(string)) //nolint:forcetypeassert,errcheck // checked by the schema
		}

		next, ok := page.data()["nextCursor"].(string)
		if !ok {
			break
		}

		cursor = next
	}

	assert.Equal(t, []string{"n5", "n4", "n3", "n2", "n1"}, titles)

	bad := a.do(http.MethodGet, "/api/v1/notes?cursor=garbage", token, "")
	require.Equal(t, http.StatusUnprocessableEntity, bad.Status)
	assert.Contains(t, bad.Raw, `"field":"query.cursor"`)

	tooMany := a.do(http.MethodGet, "/api/v1/notes?limit=101", token, "")
	require.Equal(t, http.StatusUnprocessableEntity, tooMany.Status)
	assert.Contains(t, tooMany.Raw, `"field":"query.limit","rule":"max"`)
}

func TestNotesRejectInvalidRequests(t *testing.T) {
	t.Parallel()

	a := notesApp(t)
	token := testutil.IssueAccessToken(t, nil)

	missing := a.do(http.MethodPost, "/api/v1/notes", token, `{"content":"x"}`, "Accept-Language", "en")
	require.Equal(t, http.StatusUnprocessableEntity, missing.Status)
	assert.Contains(t, missing.Raw, `{"field":"title","rule":"required","message":"is required"}`)

	for _, body := range []string{`{}`, `{"foo":1}`} {
		empty := a.do(http.MethodPatch, "/api/v1/notes/"+uuid.NewV7().String(), token, body, "Accept-Language", "en")
		require.Equal(t, http.StatusUnprocessableEntity, empty.Status, body)
		assert.Contains(t, empty.Raw, `{"field":"body","rule":"minProperties","message":"must have at least 1 field(s)","params":{"minProperties":1}}`, body)
	}

	badID := a.do(http.MethodGet, "/api/v1/notes/nope", token, "")
	require.Equal(t, http.StatusUnprocessableEntity, badID.Status)
	assert.Contains(t, badID.Raw, `"field":"path.id","rule":"uuid"`)

	anonymous := a.do(http.MethodGet, "/api/v1/notes", "", "")
	assert.Equal(t, http.StatusUnauthorized, anonymous.Status)
	assert.Equal(t, "Bearer", anonymous.Header.Get("WWW-Authenticate"))

	expired := testutil.IssueAccessToken(t, &testutil.TokenOptions{TTL: -time.Hour})
	r := a.do(http.MethodGet, "/api/v1/notes", expired, "")
	assert.Equal(t, "auth.tokenExpired", r.Body["errorKey"])

	assert.Equal(t, http.StatusUnsupportedMediaType, a.do(http.MethodPost, "/api/v1/notes", token, `x`, "Content-Type", "text/plain").Status)
	assert.Equal(t, http.StatusBadRequest, a.do(http.MethodPost, "/api/v1/notes", token, `{"title":`).Status)
	assert.Equal(t, http.StatusMethodNotAllowed, a.do(http.MethodPut, "/api/v1/notes", token, "").Status)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/notes/", token, "").Status)
}

func TestNotesIdempotencyKeyUsesRedis(t *testing.T) {
	t.Parallel()

	a := notesApp(t)
	user := uuid.NewV7()
	token := testutil.IssueAccessToken(t, &testutil.TokenOptions{Subject: user})

	first := a.do(http.MethodPost, "/api/v1/notes", token, `{"title":"once"}`, "Idempotency-Key", "abc")
	require.Equal(t, http.StatusCreated, first.Status)

	again := a.do(http.MethodPost, "/api/v1/notes", token, `{"title":"once"}`, "Idempotency-Key", "abc")
	assert.Equal(t, http.StatusCreated, again.Status)
	assert.JSONEq(t, first.Raw, again.Raw)

	mismatch := a.do(http.MethodPost, "/api/v1/notes", token, `{"title":"twice"}`, "Idempotency-Key", "abc")
	assert.Equal(t, "common.idempotencyKeyMismatch", mismatch.Body["errorKey"])

	var n int
	require.NoError(t, a.infra.Pool.QueryRow(t.Context(), `SELECT count(*) FROM note WHERE owner_sub = $1`, user).Scan(&n))
	assert.Equal(t, 1, n)

	key := "app-test:idem:u:" + user.String() + ":POST:/api/v1/notes:abc"
	ttl, err := a.infra.Redis.TTL(t.Context(), key).Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, 23*time.Hour)
}

func TestRevokedSessionsAndRateLimitsUseTheSharedKeys(t *testing.T) {
	t.Parallel()

	a := notesApp(t)
	sid := uuid.NewV7()
	token := testutil.IssueAccessToken(t, &testutil.TokenOptions{SessionID: sid})

	ok := a.do(http.MethodGet, "/api/v1/notes", token, "")
	require.Equal(t, http.StatusOK, ok.Status)
	assert.Equal(t, "60", ok.Header.Get("X-RateLimit-Limit"))
	assert.Equal(t, "59", ok.Header.Get("X-RateLimit-Remaining"))

	ipKey, err := a.infra.Redis.Get(t.Context(), "rl:app-test-ip:ip:127.0.0.1").Result()
	require.NoError(t, err)
	assert.Regexp(t, `^\d{13}$`, ipKey)

	require.NoError(t, a.infra.Redis.Set(t.Context(), "auth:revoked:sid:"+sid.String(), "1", time.Minute).Err())

	revoked := a.do(http.MethodGet, "/api/v1/notes", token, "")
	assert.Equal(t, http.StatusUnauthorized, revoked.Status)
	assert.Equal(t, `Bearer error="invalid_token"`, revoked.Header.Get("WWW-Authenticate"))
}

func TestRedisFailureMakesAuthenticationUnavailable(t *testing.T) {
	t.Parallel()

	a := newApp(t, func(c *config.Config) { c.Redis.URL = "redis://127.0.0.1:1/0" }, nil)

	r := a.do(http.MethodGet, "/api/v1/notes", testutil.IssueAccessToken(t, nil), "")
	assert.Equal(t, http.StatusServiceUnavailable, r.Status)
	assert.Equal(t, "common.serviceUnavailable", r.Body["errorKey"])
	assert.NotEmpty(t, r.Header.Get("Retry-After"))
}

func TestServeFinishesInFlightRequestsAfterCancel(t *testing.T) {
	t.Parallel()

	cfg := loadConfig(t, _db.Clone(t))
	cfg.App.Port = freePort(t)
	cfg.App.ShutdownTimeout = 10 * time.Second

	obs, err := app.SetupObservability(t.Context(), cfg, "test")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx, cfg, slog.New(slog.DiscardHandler), obs) }()

	base := "http://127.0.0.1:" + strconv.Itoa(cfg.App.Port)
	waitReady(t, base)

	pr, pw := io.Pipe()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/api/v1/notes", pr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testutil.IssueAccessToken(t, nil))

	responses := make(chan int, 1)

	go func() {
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			responses <- 0

			return
		}
		defer res.Body.Close()

		responses <- res.StatusCode
	}()

	_, err = pw.Write([]byte(`{"title":`))
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	_, err = pw.Write([]byte(`"late"}`))
	require.NoError(t, err)
	require.NoError(t, pw.Close())

	assert.Equal(t, http.StatusCreated, <-responses)
	require.NoError(t, <-done)
}
