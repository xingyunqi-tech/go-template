//go:build integration

package app_test

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/usecase/address"
	"github.com/xingyunqi-tech/go-template/internal/usecase/favorite"
	"github.com/xingyunqi-tech/go-template/internal/usecase/feedback"
)

const _feedbacks = "/api/v1/feedbacks"

func insertFeedback(t *testing.T, a *testApp, email, kind string) string {
	t.Helper()

	var id int

	require.NoError(t, a.pool.QueryRow(t.Context(), `INSERT INTO "feedback" ("userId", "type", "content", "questionVersion", "reason")
		SELECT "id", $2, 'r', 3, 'other' FROM "user" WHERE "email" = $1 RETURNING "id"`, email, kind).Scan(&id))

	return _feedbacks + "/" + strconv.Itoa(id)
}

func TestFeedbackLifecycle(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00).access
	admin := a.login("admin@example.com", _secret00).access

	bug := a.do(http.MethodPost, _feedbacks, john, map[string]any{"type": "bug", "content": "c"})
	require.Equal(t, http.StatusOK, bug.Status, bug.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"id":\d+,"userId":\d+,"type":"bug","content":"c",`+
		`"contact":null,"status":"pending","reply":null,"questionId":null,"questionVersion":null,"reason":null,`+
		`"errataId":null,"messageId":null,"messageSnapshot":null,"createdAt":"[^"]+","updatedAt":"[^"]+"\}\}$`, bug.Raw)

	withImages := a.do(http.MethodPost, _feedbacks, john, map[string]any{
		"type": "suggestion", "content": "<b>s</b>", "images": []string{"a", "b"}, "contact": "x", "unknown": 1,
	})
	assert.Contains(t, withImages.Raw, `"content":"<b>s</b>","images":["a","b"],"contact":"x","status":"pending"`)

	empty := a.do(http.MethodPost, _feedbacks, john, map[string]any{"type": "other", "content": "o", "images": []string{}})
	assert.Contains(t, empty.Raw, `"images":[],"contact":null`)

	assert.Equal(t, bug.data(), a.do(http.MethodGet, idPath(_feedbacks, bug), john, nil).data())
	assert.JSONEq(t, `{"code":404,"message":"Feedback not found","data":null}`, a.do(http.MethodGet, idPath(_feedbacks, bug), admin, nil).Raw)

	list, total := pageOf(t, a.do(http.MethodGet, _feedbacks, john, nil))
	assert.InDelta(t, 3, total, 0)
	assert.Equal(t, empty.data()["id"], firstItem(t, list)["id"])

	list, total = pageOf(t, a.do(http.MethodGet, _feedbacks+"?type=bug", john, nil))
	assert.InDelta(t, 1, total, 0)
	assert.Len(t, list, 1)

	assert.Contains(t, a.do(http.MethodGet, _feedbacks+"?page=2&limit=1.5", john, nil).Raw, `"total":2.5,"page":2,"limit":1.5,"totalPages":2}`)
	assert.Contains(t, a.do(http.MethodGet, _feedbacks+"?limit=100", john, nil).Raw, `"limit":50,`)

	report := insertFeedback(t, a, "john.doe@example.com", "question_error")
	ai := insertFeedback(t, a, "john.doe@example.com", "ai_content")
	assert.Equal(t, http.StatusOK, a.do(http.MethodGet, report, john, nil).Status)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, ai, john, nil).Status)

	_, total = pageOf(t, a.do(http.MethodGet, _feedbacks, john, nil))
	assert.InDelta(t, 3, total, 0)

	_, total = pageOf(t, a.do(http.MethodGet, _feedbacks+"?type=question_error", john, nil))
	assert.InDelta(t, 1, total, 0)

	assert.Equal(t, http.StatusUnprocessableEntity, a.do(http.MethodPost, _feedbacks, john, map[string]any{"type": "question_error", "content": "x"}).Status)
	assert.Equal(t, http.StatusUnprocessableEntity, a.do(http.MethodGet, _feedbacks+"?type=ai_content", john, nil).Status)
}

func TestAccountCleanersRemoveUserData(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00).access

	for _, r := range []*result{
		a.do(http.MethodPost, _addresses, john, addressBody("A", map[string]any{"province": "P", "city": "C", "district": "D"})),
		a.do(http.MethodPost, _favorites, john, map[string]any{"targetType": "product", "targetId": "p1"}),
		a.do(http.MethodPost, _feedbacks, john, map[string]any{"type": "bug", "content": "c", "images": []string{"a"}, "contact": "x"}),
	} {
		require.Equal(t, http.StatusOK, r.Status, r.Raw)
	}

	var userID int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "id" FROM "user" WHERE "email" = 'john.doe@example.com'`).Scan(&userID))

	db := postgres.NewDB(a.pool, time.Second, slog.New(slog.DiscardHandler))
	cleaners := []interface {
		Clean(ctx context.Context, userID int) error
	}{
		favorite.NewCleaner(postgres.NewFavorites(db)), feedback.NewCleaner(postgres.NewFeedbacks(db)),
		address.NewCleaner(postgres.NewAddresses(db)),
	}

	for range 2 {
		for _, c := range cleaners {
			require.NoError(t, c.Clean(t.Context(), userID))
		}
	}

	var addresses, favorites, owned, anonymized int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM "address" WHERE "userId" = $1),
		(SELECT count(*) FROM "favorite" WHERE "userId" = $1),
		(SELECT count(*) FROM "feedback" WHERE "userId" = $1),
		(SELECT count(*) FROM "feedback" WHERE "userId" IS NULL AND "contact" IS NULL AND "images" IS NULL)`, userID).
		Scan(&addresses, &favorites, &owned, &anonymized))
	assert.Zero(t, addresses)
	assert.Zero(t, favorites)
	assert.Zero(t, owned)
	assert.Equal(t, 1, anonymized)
}
