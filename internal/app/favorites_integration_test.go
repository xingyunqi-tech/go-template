//go:build integration

package app_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _favorites = "/api/v1/favorites"

func TestFavoriteLifecycle(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00).access
	admin := a.login("admin@example.com", _secret00).access
	product := map[string]any{
		"targetType": "product", "targetId": "p1", "title": " <b>Phone</b> ", "image": "https://example.com/a.png",
		"extra": "{}", "unknown": 1,
	}

	created := a.do(http.MethodPost, _favorites, john, product)
	require.Equal(t, http.StatusOK, created.Status, created.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"id":\d+,"userId":\d+,"targetType":"product","targetId":"p1",`+
		`"title":"Phone","image":"https://example.com/a.png","extra":"\{\}","subjectId":null,"createdAt":"[^"]+"\}\}$`, created.Raw)

	article := a.do(http.MethodPost, _favorites, john, map[string]any{"targetType": "article", "targetId": "a1"})
	assert.Contains(t, article.Raw, `"title":null,"image":null,"extra":null,"subjectId":null`)

	assert.JSONEq(t, `{"code":409,"message":"已收藏","data":null}`, a.do(http.MethodPost, _favorites, john, product, "Accept-Language", "zh").Raw)

	check := a.do(http.MethodPost, _favorites+"/check", john, product)
	assert.Equal(t, map[string]any{"isFavorited": true, "favoriteId": created.data()["id"]}, check.data())
	assert.JSONEq(t, `{"code":200,"message":"success","data":{"isFavorited":false}}`,
		a.do(http.MethodPost, _favorites+"/check", admin, product).Raw)

	store := map[string]any{"targetType": "store", "targetId": "s1", "title": ""}
	on := a.do(http.MethodPost, _favorites+"/toggle", john, store)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"isFavorited":true,"favorite":\{"id":\d+,`, on.Raw)

	list, total := pageOf(t, a.do(http.MethodGet, _favorites, john, nil))
	assert.InDelta(t, 3, total, 0)
	require.Len(t, list, 3)
	assert.Contains(t, list[0], "targetOffline")
	assert.Nil(t, firstItem(t, list)["targetOffline"])

	list, total = pageOf(t, a.do(http.MethodGet, _favorites+"?targetType=store&limit=1", john, nil))
	assert.InDelta(t, 1, total, 0)
	assert.Len(t, list, 1)

	assert.JSONEq(t, `{"code":200,"message":"success","data":{"isFavorited":false}}`,
		a.do(http.MethodPost, _favorites+"/toggle", john, store).Raw)

	assert.Equal(t, http.StatusNotFound, a.do(http.MethodDelete, idPath(_favorites, created), admin, nil).Status)
	require.Equal(t, http.StatusOK, a.do(http.MethodDelete, idPath(_favorites, created), john, nil).Status)
	assert.JSONEq(t, `{"code":404,"message":"Favorite not found","data":null}`, a.do(http.MethodDelete, idPath(_favorites, created), john, nil).Raw)
}

func TestFavoriteExamTargets(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00).access
	question := map[string]any{"targetType": "question", "targetId": "00000000-0000-4000-8000-00000000000A"}
	paper := map[string]any{"targetType": "paper", "targetId": "p1"}

	for _, path := range []string{_favorites, _favorites + "/toggle"} {
		q := a.do(http.MethodPost, path, john, question)
		assert.JSONEq(t, `{"code":403,"message":"This question is not accessible","data":{"errorKey":"questionNotAccessible"}}`, q.Raw)
		assert.Equal(t, http.StatusNotFound, a.do(http.MethodPost, path, john, paper).Status)
	}

	assert.JSONEq(t, `{"code":200,"message":"success","data":{"isFavorited":false}}`, a.do(http.MethodPost, _favorites+"/check", john, question).Raw)

	_, err := a.pool.Exec(t.Context(), `INSERT INTO "favorite" ("userId", "targetType", "targetId")
		SELECT "id", 'question', 'q1' FROM "user" WHERE "email" = 'john.doe@example.com'`)
	require.NoError(t, err)

	list, _ := pageOf(t, a.do(http.MethodGet, _favorites+"?targetType=question", john, nil))
	require.Len(t, list, 1)
	assert.Equal(t, true, firstItem(t, list)["targetOffline"])

	assert.Equal(t, http.StatusUnprocessableEntity, a.do(http.MethodGet, _favorites+"?targetType=bad", john, nil).Status)
	assert.Equal(t, http.StatusUnprocessableEntity, a.do(http.MethodPost, _favorites, john, map[string]any{"targetType": "movie", "targetId": "1"}).Status)
}
