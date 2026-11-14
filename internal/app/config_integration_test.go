//go:build integration

package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (a *testApp) exec(sql string) {
	a.t.Helper()

	_, err := a.pool.Exec(a.t.Context(), sql)
	require.NoError(a.t, err)
}

// putRaw sends a JSON body as written, keeping its key order.
func (a *testApp) putRaw(path, token, body string) *result {
	a.t.Helper()

	req, err := http.NewRequestWithContext(a.t.Context(), http.MethodPut, a.url+path, strings.NewReader(body))
	require.NoError(a.t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := http.DefaultClient.Do(req)
	require.NoError(a.t, err)

	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	require.NoError(a.t, err)

	out := &result{Status: res.StatusCode, Header: res.Header, Raw: string(raw), Body: map[string]any{}}
	_ = json.Unmarshal(raw, &out.Body)

	return out
}

func TestConfigReads(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)

	public := a.do(http.MethodGet, "/api/v1/config/ai.enabled", "", nil)
	require.Equal(t, http.StatusOK, public.Status, public.Raw)
	assert.Regexp(t, `^\{"code":200,"message":"success","data":\{"key":"ai.enabled","value":\{"value":true\},"isPublic":true,"createdAt":"[^"]+Z","updatedAt":"[^"]+Z"\}\}$`, public.Raw)

	notFound := `{"code":404,"message":"Config not found","data":null}`
	for _, key := range []string{"ai.ratePerMinute", "ai.model", "app.profile", "consent.privacyPolicyVersion", "unknown.key", "%E4%B8%AD"} {
		assert.JSONEq(t, notFound, a.do(http.MethodGet, "/api/v1/config/"+key, "", nil).Raw, key)
	}

	assert.JSONEq(t, `{"code":404,"message":"配置不存在","data":null}`, a.do(http.MethodGet, "/api/v1/config/unknown.key", "", nil, "Accept-Language", "zh").Raw)

	private := a.do(http.MethodGet, "/api/v1/config/admin/ai.ratePerMinute", admin.access, nil)
	require.Equal(t, http.StatusOK, private.Status, private.Raw)
	assert.Contains(t, private.Raw, `"key":"ai.ratePerMinute","value":{"value":10},"isPublic":false`)
	assert.JSONEq(t, notFound, a.do(http.MethodGet, "/api/v1/config/admin/unknown.key", admin.access, nil).Raw)
}

func TestConfigRegisteredDefaultsAndDefinitionVisibility(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)

	a.exec(`DELETE FROM "config" WHERE "key" = 'ai.quota.chat'`)
	a.exec(`UPDATE "config" SET "isPublic" = false WHERE "key" = 'ai.enabled'`)

	assert.JSONEq(t,
		`{"code":200,"message":"success","data":{"key":"ai.quota.chat","value":{"value":20},"isPublic":true,"createdAt":null,"updatedAt":null}}`,
		a.do(http.MethodGet, "/api/v1/config/ai.quota.chat", "", nil).Raw)

	enabled := a.do(http.MethodGet, "/api/v1/config/ai.enabled", "", nil)
	require.Equal(t, http.StatusOK, enabled.Status, enabled.Raw)
	assert.Equal(t, true, enabled.data()["isPublic"])

	written := a.do(http.MethodPut, "/api/v1/config/ai.ratePerMinute", admin.access, map[string]any{"value": map[string]any{"value": 11}, "isPublic": true})
	require.Equal(t, http.StatusOK, written.Status, written.Raw)
	assert.Equal(t, false, written.data()["isPublic"])
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/config/ai.ratePerMinute", "", nil).Status)

	var stored bool
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "isPublic" FROM "config" WHERE "key" = 'ai.ratePerMinute'`).Scan(&stored))
	assert.False(t, stored)
}

func TestConfigPermissionsAndValidation(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)
	john := a.login("john.doe@example.com", _secret00)

	forbidden := `{"code":403,"message":"Missing permissions: system:config","data":null}`
	assert.JSONEq(t, forbidden, a.do(http.MethodGet, "/api/v1/config/admin/ai.enabled", john.access, nil).Raw)
	assert.JSONEq(t, forbidden, a.do(http.MethodPut, "/api/v1/config/p.denied", john.access, map[string]any{"value": map[string]any{}}).Raw)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodGet, "/api/v1/config/admin/ai.enabled", "", nil).Status)
	assert.Equal(t, http.StatusUnauthorized, a.do(http.MethodPut, "/api/v1/config/p.denied", "", map[string]any{"value": map[string]any{}}).Status)

	cases := []struct {
		key  string
		body map[string]any
		want string
	}{
		{"app.profile", map[string]any{"value": map[string]any{}}, `{"key":"This config can only be updated through its dedicated endpoint"}`},
		{"p.x", map[string]any{}, `{"value":"value must be an object"}`},
		{"p.x", map[string]any{"value": "x", "isPublic": "yes"}, `{"value":"value must be an object","isPublic":"isPublic must be a boolean value"}`},
		{"p.x", map[string]any{"value": []int{1}, "isPublic": nil}, `{"value":"value must be an object"}`},
	}

	for _, tc := range cases {
		r := a.do(http.MethodPut, "/api/v1/config/"+tc.key, admin.access, tc.body)
		assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":`+tc.want+`}`, r.Raw, tc.key)
	}

	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/config/admin/p.x", admin.access, nil).Status)
}

func TestConfigUpsertLifecycle(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)
	path := "/api/v1/config/p.lifecycle"

	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, path, "", nil).Status)

	created := a.putRaw(path, admin.access, `{"value":{"b":1,"a":{"y":2,"x":"<&>"}}}`)
	require.Equal(t, http.StatusOK, created.Status, created.Raw)
	assert.Contains(t, created.Raw, `"key":"p.lifecycle","value":{"b":1,"a":{"y":2,"x":"<&>"}},"isPublic":false,"createdAt"`)

	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, path, "", nil).Status)

	stored := a.do(http.MethodGet, "/api/v1/config/admin/p.lifecycle", admin.access, nil)
	assert.Contains(t, stored.Raw, `"value":{"a":{"x":"<&>","y":2},"b":1}`)

	same := a.putRaw(path, admin.access, `{"value":{"a":{"x":"<&>","y":2},"b":1}}`)
	require.Equal(t, http.StatusOK, same.Status, same.Raw)
	assert.Contains(t, same.Raw, `"value":{"a":{"x":"<&>","y":2},"b":1}`)
	assert.Equal(t, same.data()["createdAt"], same.data()["updatedAt"])

	published := a.do(http.MethodPut, path, admin.access, map[string]any{"value": map[string]any{"c": true}, "isPublic": true})
	require.Equal(t, http.StatusOK, published.Status, published.Raw)
	assert.Equal(t, true, published.data()["isPublic"])
	assert.NotEqual(t, published.data()["createdAt"], published.data()["updatedAt"])

	visible := a.do(http.MethodGet, path, "", nil)
	require.Equal(t, http.StatusOK, visible.Status, visible.Raw)
	assert.Equal(t, map[string]any{"c": true}, visible.data()["value"])

	kept := a.do(http.MethodPut, path, admin.access, map[string]any{"value": map[string]any{"c": false}})
	assert.Equal(t, true, kept.data()["isPublic"])
	assert.Equal(t, map[string]any{"c": false}, a.do(http.MethodGet, path, "", nil).data()["value"])

	encoded := a.do(http.MethodPut, "/api/v1/config/%E4%B8%AD", admin.access, map[string]any{"value": map[string]any{}, "isPublic": true})
	assert.Equal(t, "中", encoded.data()["key"])
	assert.Equal(t, "中", a.do(http.MethodGet, "/api/v1/config/%E4%B8%AD", "", nil).data()["key"])
}

func TestConfigReadsAreCachedUntilWritten(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)
	path := "/api/v1/config/ai.quota.grading"

	require.Equal(t, http.StatusOK, a.do(http.MethodGet, path, "", nil).Status)
	a.exec(`UPDATE "config" SET "value" = '{"value": 7}' WHERE "key" = 'ai.quota.grading'`)
	assert.Equal(t, map[string]any{"value": 3.0}, a.do(http.MethodGet, path, "", nil).data()["value"])

	a.exec(`DELETE FROM "config" WHERE "key" = 'ai.dailyBudget'`)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/config/admin/ai.dailyBudget", admin.access, nil).Status)
	a.exec(`INSERT INTO "config" ("key", "value") VALUES ('ai.dailyBudget', '{"value": 100}')`)
	assert.Equal(t, http.StatusNotFound, a.do(http.MethodGet, "/api/v1/config/admin/ai.dailyBudget", admin.access, nil).Status)

	written := a.do(http.MethodPut, path, admin.access, map[string]any{"value": map[string]any{"value": 4}})
	require.Equal(t, http.StatusOK, written.Status, written.Raw)
	assert.Equal(t, map[string]any{"value": 4.0}, a.do(http.MethodGet, path, "", nil).data()["value"])
}
