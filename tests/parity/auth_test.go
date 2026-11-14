//go:build parity

package parity_test

import (
	"crypto/md5" //nolint:gosec // clients send MD5 passwords
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var _run = strconv.FormatInt(time.Now().UnixMilli(), 10) //nolint:gochecknoglobals // unique per test run

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // see import

	return hex.EncodeToString(sum[:])
}

func volatileKeys() map[string]bool {
	return map[string]bool{
		"token": true, "refreshToken": true, "tokenExpires": true, "createdAt": true,
		"updatedAt": true, "lastActiveAt": true, "ip": true,
	}
}

// normalize hides values that differ between the two services: tokens, timestamps, IPs and
// numeric ids.
func normalize(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = normalize(item, k)
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalize(item, "")
		}

		return out
	}

	if volatileKeys()[key] && v != nil {
		return fmt.Sprintf("<%T>", v)
	}

	if _, isNumber := v.(float64); isNumber && key == "id" {
		return "<id>"
	}

	return v
}

func requireSameBody(t *testing.T, p pair) {
	t.Helper()

	require.Equal(t, p.Nestjs.Status, p.Go.Status, "go: %s\nnestjs: %s", p.Go.Body, p.Nestjs.Body)
	require.Equal(t, p.Nestjs.Header.Get("Content-Type"), p.Go.Header.Get("Content-Type"))
	require.Equal(t, normalize(p.Nestjs.JSON(t), ""), normalize(p.Go.JSON(t), ""))
}

func dataOf(t *testing.T, r *response) map[string]any {
	t.Helper()

	body, ok := r.JSON(t).(map[string]any)
	require.True(t, ok, string(r.Body))

	data, ok := body["data"].(map[string]any)
	require.True(t, ok, string(r.Body))

	return data
}

func password() string {
	return md5Hex("Secret00")
}

// loginBoth signs in on both services and returns the access tokens.
func loginBoth(t *testing.T, email string) [2]string {
	t.Helper()

	p := do(t, http.MethodPost, "/api/v1/auth/email/login", withJSON(t, map[string]any{"email": email, "password": password()}))
	require.Equal(t, http.StatusOK, p.Go.Status, string(p.Go.Body))
	require.Equal(t, http.StatusOK, p.Nestjs.Status, string(p.Nestjs.Body))

	goToken, _ := dataOf(t, p.Go)["token"].(string)
	nestToken, _ := dataOf(t, p.Nestjs)["token"].(string)

	return [2]string{goToken, nestToken}
}

// tempUser registers the same new email on both services and signs it in.
func tempUser(t *testing.T, prefix string) [2]string {
	t.Helper()

	email := "parity." + prefix + "." + _run + "@example.com"
	body := map[string]any{"email": email, "password": password(), "firstName": "P", "lastName": "T"}

	p := do(t, http.MethodPost, "/api/v1/auth/email/register", withJSON(t, body))
	require.Equal(t, http.StatusOK, p.Go.Status, string(p.Go.Body))
	require.Equal(t, http.StatusOK, p.Nestjs.Status, string(p.Nestjs.Body))

	return loginBoth(t, email)
}

func TestEmailLogin(t *testing.T) {
	t.Parallel()

	bodies := []map[string]any{
		{},
		{"email": "admin@example.com", "password": password()},
		{"email": " ADMIN@Example.com ", "password": strings.ToUpper(password())},
		{"email": "missing@example.com", "password": password()},
		{"email": "admin@example.com", "password": md5Hex("wrong")},
		{"email": "not-an-email", "password": "short"},
		{"email": 123, "password": nil},
		{"email": "admin@example.com", "password": password(), "extra": true},
	}

	for _, lang := range []string{"en", "zh-CN"} {
		for i, body := range bodies {
			t.Run(lang+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()

				requireSameBody(t, do(t, http.MethodPost, "/api/v1/auth/email/login", withJSON(t, body), withHeader("Accept-Language", lang)))
			})
		}
	}
}

func TestEmailRegister(t *testing.T) {
	t.Parallel()

	bodies := []map[string]any{
		{},
		{"email": "john.doe@example.com", "password": password(), "firstName": "J", "lastName": "D"},
		{"email": "parity." + _run + "@example.com", "password": password(), "firstName": "<b>P</b>", "lastName": "R"},
		{"email": "x@example.com", "password": password(), "firstName": "", "lastName": 1},
	}

	for i, body := range bodies {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			requireSameBody(t, do(t, http.MethodPost, "/api/v1/auth/email/register", withJSON(t, body)))
		})
	}
}

func TestEmailLinkErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		body map[string]any
	}{
		{"/api/v1/auth/email/confirm", map[string]any{"hash": "invalid"}},
		{"/api/v1/auth/email/confirm", map[string]any{}},
		{"/api/v1/auth/email/confirm/new", map[string]any{"hash": "a.b.c"}},
		{"/api/v1/auth/forgot/password", map[string]any{"email": "missing@example.com"}},
		{"/api/v1/auth/forgot/password", map[string]any{"email": "bad"}},
		{"/api/v1/auth/reset/password", map[string]any{"hash": "invalid", "password": password()}},
		{"/api/v1/auth/reset/password", map[string]any{"hash": "x", "password": "plain"}},
	}

	for i, c := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			requireSameBody(t, do(t, http.MethodPost, c.url, withJSON(t, c.body)))
		})
	}
}

func TestUnauthenticated(t *testing.T) {
	t.Parallel()

	headers := []map[string]string{
		{},
		{"Authorization": "Bearer nope"},
		{"Authorization": "Basic abc"},
		{"Authorization": "Bearer"},
	}
	routes := [][2]string{
		{"GET", "/api/v1/auth/me"},
		{"POST", "/api/v1/auth/refresh"},
		{"GET", "/api/v1/auth/sessions"},
		{"POST", "/api/v1/auth/logout"},
		{"GET", "/api/v1/users"},
		{"DELETE", "/api/v1/users/abc"},
	}

	for _, r := range routes {
		for i, h := range headers {
			t.Run(r[0]+" "+r[1]+"/"+strconv.Itoa(i), func(t *testing.T) {
				t.Parallel()

				opts := []option{withHeader("Accept-Language", "zh")}
				for k, v := range h {
					opts = append(opts, withHeader(k, v))
				}

				requireSameBody(t, do(t, r[0], r[1], opts...))
			})
		}
	}
}

func TestMeAndSessions(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")
	requireSameBody(t, do(t, http.MethodGet, "/api/v1/auth/me", withTokens(tokens)))

	p := do(t, http.MethodGet, "/api/v1/auth/sessions", withTokens(tokens))
	require.Equal(t, http.StatusOK, p.Go.Status)
	require.Equal(t, http.StatusOK, p.Nestjs.Status)

	first := func(r *response) []string {
		body, _ := r.JSON(t).(map[string]any)
		list, _ := body["data"].([]any)
		require.NotEmpty(t, list)

		item, _ := list[0].(map[string]any)

		return keys(item)
	}
	require.ElementsMatch(t, first(p.Nestjs), first(p.Go))
}

func TestUpdateMeErrors(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "profile")
	bodies := []map[string]any{
		{"firstName": 123, "password": md5Hex("x")},
		{"password": md5Hex("x"), "oldPassword": md5Hex("wrong")},
		{"email": "john.doe@example.com"},
		{"gender": "male", "birthday": "1990/01/01"},
		{"photo": map[string]any{"id": "not-uuid"}},
		{"photo": map[string]any{"id": "3f0c8a52-7d0e-4b59-9f8e-2a1d6c4b7e10"}},
	}

	for _, body := range bodies {
		requireSameBody(t, do(t, http.MethodPut, "/api/v1/auth/me", withTokens(tokens), withJSON(t, body)))
	}

	for _, body := range []map[string]any{
		{},
		{"oldPassword": md5Hex("wrong"), "newPassword": password()},
		{"oldPassword": "x", "newPassword": "y"},
	} {
		requireSameBody(t, do(t, http.MethodPut, "/api/v1/auth/password", withTokens(tokens), withJSON(t, body)))
	}
}

func TestRefreshRotation(t *testing.T) {
	t.Parallel()

	p := do(t, http.MethodPost, "/api/v1/auth/email/login", withJSON(t, map[string]any{"email": "john.doe@example.com", "password": password()}))
	goRefresh, _ := dataOf(t, p.Go)["refreshToken"].(string)
	nestRefresh, _ := dataOf(t, p.Nestjs)["refreshToken"].(string)
	refresh := [2]string{goRefresh, nestRefresh}

	requireSameBody(t, do(t, http.MethodPost, "/api/v1/auth/refresh", withTokens(refresh)))
	requireSameBody(t, do(t, http.MethodPost, "/api/v1/auth/refresh", withTokens(refresh)))
}

func TestRevokeUnknownSession(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "john.doe@example.com")
	requireSameBody(t, do(t, http.MethodDelete, "/api/v1/auth/sessions/999999", withTokens(tokens)))
	requireSameBody(t, do(t, http.MethodDelete, "/api/v1/auth/sessions/0", withTokens(tokens)))
}

func sortUsersByEmail(t *testing.T, v any) any {
	t.Helper()

	body, ok := v.(map[string]any)
	if !ok {
		return v
	}

	page, ok := body["data"].(map[string]any)
	if !ok {
		return v
	}

	users, ok := page["data"].([]any)
	if !ok {
		return v
	}

	sort.SliceStable(users, func(i, j int) bool {
		a, _ := users[i].(map[string]any)
		b, _ := users[j].(map[string]any)

		return fmt.Sprint(a["email"]) < fmt.Sprint(b["email"])
	})

	return v
}

func TestAdminReads(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")
	urls := []string{
		"/api/v1/users?limit=2",
		"/api/v1/users?page=1&limit=1&sort=%5B%7B%22orderBy%22%3A%22id%22%2C%22order%22%3A%22desc%22%7D%5D",
		"/api/v1/users?limit=0",
		"/api/v1/users?page=abc",
		"/api/v1/users?filters=%7B%22roles%22%3A%5B%7B%22id%22%3A%22x%22%7D%5D%7D",
		"/api/v1/users?filters=notjson",
		"/api/v1/users/1",
		"/api/v1/users/999999",
		"/api/v1/users/2147483648",
	}

	for _, url := range urls {
		t.Run(url, func(t *testing.T) {
			t.Parallel()

			p := do(t, http.MethodGet, url, withTokens(tokens))
			if strings.Contains(url, "sort=") || !strings.Contains(url, "/users?") {
				requireSameBody(t, p)

				return
			}

			require.Equal(t, p.Nestjs.Status, p.Go.Status)
			require.Equal(t,
				normalize(sortUsersByEmail(t, p.Nestjs.JSON(t)), ""),
				normalize(sortUsersByEmail(t, p.Go.JSON(t)), ""))
		})
	}
}

func TestUsersForbiddenForUserRole(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "john.doe@example.com")
	requireSameBody(t, do(t, http.MethodGet, "/api/v1/users", withTokens(tokens)))
	requireSameBody(t, do(t, http.MethodDelete, "/api/v1/users/abc", withTokens(tokens)))
}

func TestAdminCreateErrors(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")
	email := "admin.made." + _run + "@example.com"
	bodies := []map[string]any{
		{},
		{"email": "john.doe@example.com", "password": password()},
		{"email": email, "role": map[string]any{"id": 9}},
		{"email": email, "status": map[string]any{"id": 7}},
		{"email": email, "photo": map[string]any{"id": "3f0c8a52-7d0e-4b59-9f8e-2a1d6c4b7e10"}},
	}

	for _, body := range bodies {
		requireSameBody(t, do(t, http.MethodPost, "/api/v1/users", withTokens(tokens), withJSON(t, body)))
	}
}

func TestAdminCreateUpdateDelete(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")
	body := map[string]any{
		"email": "lifecycle." + _run + "@example.com", "password": password(), "firstName": "Life",
		"role": map[string]any{"id": 2}, "status": map[string]any{"id": 1}, "birthday": "1990-05-06", "gender": 2,
	}

	created := do(t, http.MethodPost, "/api/v1/users", withTokens(tokens), withJSON(t, body))
	requireSameBody(t, created)

	goID, _ := dataOf(t, created.Go)["id"].(float64)
	nestID, _ := dataOf(t, created.Nestjs)["id"].(float64)
	paths := [2]string{fmt.Sprintf("/api/v1/users/%d", int(goID)), fmt.Sprintf("/api/v1/users/%d", int(nestID))}
	user := func(method string, options ...option) pair {
		return do(t, method, paths[0], append([]option{withTokens(tokens), withTargets(paths)}, options...)...)
	}

	requireSameBody(t, user(http.MethodPut, withJSON(t, map[string]any{"nickname": "nick", "phone": "139" + _run[len(_run)-8:], "firstName": nil})))
	requireSameBody(t, user(http.MethodPut, withJSON(t, map[string]any{"email": "admin@example.com"})))
	requireSameBody(t, user(http.MethodDelete))
	requireSameBody(t, user(http.MethodGet))
	requireSameBody(t, user(http.MethodPut, withJSON(t, map[string]any{"nickname": "after"})))
}
