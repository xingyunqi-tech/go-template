package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpapi "github.com/xingyunqi-tech/go-template/internal/controller/http"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/pkg/ratelimit"
)

// _panicStatus asks the events handler to panic.
const _panicStatus = 999

// journal records the steps of the requests to /api/v1/events in order.
type journal struct {
	mu        sync.Mutex
	events    []string
	abort     bool
	failAudit bool
	gate      chan struct{}
	entered   chan struct{}
}

func (j *journal) add(e string) {
	j.mu.Lock()
	defer j.mu.Unlock()

	j.events = append(j.events, e)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()

	return append([]string(nil), j.events...)
}

func (j *journal) count(e string) int {
	n := 0

	for _, got := range j.list() {
		if got == e {
			n++
		}
	}

	return n
}

type journalKey struct{}

func (j *journal) Begin(ctx context.Context) (context.Context, httpapi.Tx, error) {
	j.add("begin")

	return context.WithValue(ctx, journalKey{}, j), journalTx{j: j}, nil
}

type journalTx struct{ j *journal }

func (t journalTx) Commit(context.Context) (bool, error) {
	if t.j.abort {
		t.j.add("aborted")

		return false, nil
	}

	t.j.add("commit")

	return true, nil
}

func (t journalTx) Rollback(context.Context) { t.j.add("rollback") }

func (j *journal) Log(_ context.Context, l *repo.AuditLog) error {
	if j.failAudit {
		return errors.New("audit insert failed")
	}

	j.add("audit " + l.Action)

	return nil
}

func eventHandler(writeError func(http.ResponseWriter, *http.Request, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		j, _ := r.Context().Value(journalKey{}).(*journal)
		j.add("handler")

		if j.gate != nil {
			j.entered <- struct{}{}

			<-j.gate
		}

		body, _ := io.ReadAll(r.Body)

		var in struct {
			Status int `json:"status"`
		}

		_ = json.Unmarshal(body, &in)

		switch {
		case in.Status == _panicStatus:
			panic("handler panicked")
		case in.Status == http.StatusConflict:
			writeError(w, r, entity.New(entity.KeyConflict))
		case in.Status >= http.StatusInternalServerError:
			writeError(w, r, errors.New("handler failed"))
		default:
			envelope(w, http.StatusCreated, map[string]any{"id": "019a8f3c-6b1e-7d2a-9c4f-1e2d3c4b5a69"})
		}
	}
}

// orderedWriter records "response" in the journal when the status line is written.
type orderedWriter struct {
	*httptest.ResponseRecorder

	j *journal
}

func (w orderedWriter) WriteHeader(status int) {
	w.j.add("response")
	w.ResponseRecorder.WriteHeader(status)
}

func newJournalHarness(t *testing.T) (*harness, *journal) {
	t.Helper()

	j := &journal{}
	h := newHarness(t, true, func(o *httpapi.Options) {
		o.Transactor = j
		o.Auditor = j
	})

	return h, j
}

func postEvent(t *testing.T, h *harness, j *journal, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/events", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("Content-Type", "application/json")

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	w := orderedWriter{ResponseRecorder: httptest.NewRecorder(), j: j}
	h.handler.ServeHTTP(w, req)

	return w.ResponseRecorder
}

func TestTransactionCommitsBeforeTheResponseAndAuditsInside(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)

	r := postEvent(t, h, j, `{"status":201}`)
	assert.Equal(t, http.StatusCreated, r.Code)
	assert.Equal(t, []string{"begin", "handler", "audit event.create", "commit", "response"}, j.list())
}

func TestTransactionCommitsA4xxWithoutAudit(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)

	r := postEvent(t, h, j, `{"status":409}`)
	assert.Equal(t, http.StatusConflict, r.Code)
	assert.Equal(t, []string{"begin", "handler", "commit", "response"}, j.list())
}

func TestTransactionRollsBackA5xx(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)

	r := postEvent(t, h, j, `{"status":500}`)
	assert.Equal(t, http.StatusInternalServerError, r.Code)
	assert.Equal(t, []string{"begin", "handler", "rollback", "response"}, j.list())
}

func TestAuditFailureRollsBackAndAnswers500(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)
	j.failAudit = true

	r := postEvent(t, h, j, `{"status":201}`)
	assert.Equal(t, http.StatusInternalServerError, r.Code)
	assert.Contains(t, r.Body.String(), "common.internalError")
	assert.Equal(t, []string{"begin", "handler", "rollback", "response"}, j.list())
}

func TestAbortedTransactionKeepsA4xxAndFailsA2xx(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)
	j.abort = true

	conflict := postEvent(t, h, j, `{"status":409}`)
	assert.Equal(t, http.StatusConflict, conflict.Code)
	assert.Contains(t, conflict.Body.String(), "common.conflict")

	created := postEvent(t, h, j, `{"status":201}`)
	assert.Equal(t, http.StatusInternalServerError, created.Code)
	assert.Contains(t, created.Body.String(), "common.internalError")
}

func TestIdempotencyKeyInProgressIsAConflict(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)
	j.gate, j.entered = make(chan struct{}), make(chan struct{})

	done := make(chan *httptest.ResponseRecorder)

	go func() { done <- postEvent(t, h, j, `{"status":201}`, "Idempotency-Key", "k1") }()

	<-j.entered

	again := postEvent(t, h, j, `{"status":201}`, "Idempotency-Key", "k1")
	assert.Equal(t, http.StatusConflict, again.Code)
	assert.Contains(t, again.Body.String(), "common.idempotencyInProgress")

	close(j.gate)
	assert.Equal(t, http.StatusCreated, (<-done).Code)
}

func TestIdempotencyKeyIsReleasedAfterA5xx(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)

	for range 2 {
		r := postEvent(t, h, j, `{"status":500}`, "Idempotency-Key", "k1")
		assert.Equal(t, http.StatusInternalServerError, r.Code)
	}

	assert.Equal(t, 2, j.count("handler"))
}

func TestRedisFailureRejectsSharedRulesAndAllowsPrivateRules(t *testing.T) {
	t.Parallel()

	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})

	t.Cleanup(func() { _ = client.Close() })

	h := newHarness(t, true, func(o *httpapi.Options) {
		o.Limiter = ratelimit.New(client)
		o.RateLimits = true
	})

	shared := h.do(http.MethodPost, "/api/v1/items", `{"title":"ab","email":"a@example.com"}`)
	require.Equal(t, http.StatusServiceUnavailable, shared.Code, shared.Body.String())
	assert.Equal(t, "common.serviceUnavailable", shared.key())

	private := h.do(http.MethodGet, "/api/v1/public", "")
	assert.Equal(t, http.StatusOK, private.Code)
}

// failingComplete is an idempotency store whose responses cannot be stored.
type failingComplete struct{ *redis.Idempotency }

func (failingComplete) Complete(context.Context, string, string, *repo.IdempotencyRecord, time.Duration) error {
	return errors.New("redis down")
}

func TestUnstoredResponseKeepsTheKeyClaimed(t *testing.T) {
	t.Parallel()

	j := &journal{}
	h := newHarness(t, true, func(o *httpapi.Options) {
		o.Transactor, o.Auditor = j, j
		o.Idempotency = failingComplete{redis.NewIdempotency(nil)}
	})

	first := postEvent(t, h, j, `{"status":201}`, "Idempotency-Key", "k1")
	assert.Equal(t, http.StatusCreated, first.Code)

	again := postEvent(t, h, j, `{"status":201}`, "Idempotency-Key", "k1")
	assert.Equal(t, http.StatusConflict, again.Code)
	assert.Equal(t, 1, j.count("handler"))
}

func TestIdempotencyKeyIsReleasedAfterAPanic(t *testing.T) {
	t.Parallel()

	h, j := newJournalHarness(t)

	for range 2 {
		r := postEvent(t, h, j, `{"status":999}`, "Idempotency-Key", "k1")
		assert.Equal(t, http.StatusInternalServerError, r.Code)
	}

	assert.Equal(t, 2, j.count("handler"))
	assert.Equal(t, 2, j.count("rollback"))
}
