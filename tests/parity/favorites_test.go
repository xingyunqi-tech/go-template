//go:build parity

package parity_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const (
	_favoritesURL = "/api/v1/favorites"
	_upperUUID    = "00000000-0000-4000-8000-00000000000A"
)

func TestFavoritesRequireAuthentication(t *testing.T) {
	t.Parallel()

	routes := [][2]string{
		{http.MethodGet, ""},
		{http.MethodPost, ""},
		{http.MethodPost, "/check"},
		{http.MethodPost, "/toggle"},
		{http.MethodDelete, "/1"},
	}

	for _, r := range routes {
		t.Run(r[0]+r[1], func(t *testing.T) {
			t.Parallel()

			requireSameOwned(t, do(t, r[0], _favoritesURL+r[1], withJSON(t, map[string]any{})))
		})
	}
}

func TestFavoriteBodyValidation(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "favorite.validation")
	bodies := []map[string]any{
		{},
		{"targetType": "movie", "targetId": "1"},
		{"targetType": "product", "targetId": strings.Repeat("x", 101)},
		{
			"targetType": "product", "targetId": 1, "title": strings.Repeat("t", 201), "image": 2,
			"extra": strings.Repeat("e", 2001),
		},
	}

	for _, lang := range []string{"en", "zh"} {
		for _, path := range []string{"", "/check", "/toggle"} {
			for i, body := range bodies {
				t.Run(lang+path+"/"+strconv.Itoa(i), func(t *testing.T) {
					t.Parallel()

					requireSameOwned(t, do(t, http.MethodPost, _favoritesURL+path, withTokens(tokens), withJSON(t, body),
						withHeader("Accept-Language", lang)))
				})
			}
		}
	}
}

func TestFavoriteListQueries(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "favorite.list")

	for _, q := range []string{
		"?targetType=bad&subjectId=x&page=0", "?page=1.5&limit=0", "?subjectId=" + _upperUUID, "?targetType=question", "?limit=100",
	} {
		t.Run(q, func(t *testing.T) {
			t.Parallel()

			requireSameOwned(t, do(t, http.MethodGet, _favoritesURL+q, withTokens(tokens)))
		})
	}
}

func TestFavoriteExamTargetsAreNotFound(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "favorite.exam")
	bodies := []map[string]any{
		{"targetType": "question", "targetId": "q1"},
		{"targetType": "question", "targetId": _upperUUID},
		{"targetType": "paper", "targetId": "p1"},
		{"targetType": "paper", "targetId": _upperUUID},
	}

	for _, lang := range []string{"en", "zh"} {
		for _, body := range bodies {
			for _, path := range []string{"", "/toggle"} {
				requireSameOwned(t, do(t, http.MethodPost, _favoritesURL+path, withTokens(tokens), withJSON(t, body),
					withHeader("Accept-Language", lang)))
			}

			requireSameOwned(t, do(t, http.MethodPost, _favoritesURL+"/check", withTokens(tokens), withJSON(t, body)))
		}
	}
}

func TestFavoriteDeleteUnknownOrInvalidID(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "favorite.ids")

	for _, path := range []string{"/abc", "/0", "/999999", "/check"} {
		requireSameOwned(t, do(t, http.MethodDelete, _favoritesURL+path, withTokens(tokens)))
	}
}

func TestFavoriteLifecycle(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "favorite.lifecycle")
	call := func(method, target string, options ...option) pair {
		return requireSameOwned(t, do(t, method, target, append([]option{withTokens(tokens)}, options...)...))
	}
	product := map[string]any{
		"targetType": "product", "targetId": "p1", "title": " <b>Phone</b> ", "image": "https://example.com/a.png",
		"extra": "{}", "unknown": 1,
	}

	first := createdIDs(t, call(http.MethodPost, _favoritesURL, withJSON(t, product)))
	call(http.MethodPost, _favoritesURL, withJSON(t, map[string]any{"targetType": "article", "targetId": "a1"}))

	for _, lang := range []string{"en", "zh"} {
		call(http.MethodPost, _favoritesURL, withJSON(t, product), withHeader("Accept-Language", lang))
	}

	call(http.MethodPost, _favoritesURL+"/check", withJSON(t, product))
	call(http.MethodPost, _favoritesURL+"/check", withJSON(t, map[string]any{"targetType": "store", "targetId": "s1"}))

	store := map[string]any{"targetType": "store", "targetId": "s1", "title": ""}
	call(http.MethodPost, _favoritesURL+"/toggle", withJSON(t, store))
	call(http.MethodGet, _favoritesURL)
	call(http.MethodGet, _favoritesURL+"?targetType=store&limit=1")
	call(http.MethodGet, _favoritesURL+"?page=2&limit=2")
	call(http.MethodPost, _favoritesURL+"/toggle", withJSON(t, store))

	targets := itemTargets(_favoritesURL, first, "")
	call(http.MethodDelete, targets[0], withTargets(targets))
	call(http.MethodDelete, targets[0], withTargets(targets))
	call(http.MethodGet, _favoritesURL)
}
