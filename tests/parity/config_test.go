//go:build parity

package parity_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const _configURL = "/api/v1/config"

// pgContainers are the PostgreSQL containers of the two services: Go first, nestjs second.
func pgContainers() [2]string {
	return [2]string{
		baseURL("PARITY_GO_PG_CONTAINER", "go-template-parity-postgres-1"),
		baseURL("PARITY_NESTJS_PG_CONTAINER", "go-template-parity-nestjs-postgres-1"),
	}
}

func runPSQL(t *testing.T, container, sql string) string {
	t.Helper()

	out, err := exec.CommandContext(context.WithoutCancel(t.Context()), "docker", "exec", container, "psql", "-U", "root", "-d", "api", "-qAtc", sql).Output()
	require.NoError(t, err, sql)

	return strings.TrimSpace(string(out))
}

// configExists reports, per side, whether the config row exists.
func configExists(t *testing.T, key string) [2]bool {
	t.Helper()

	var out [2]bool
	for i, c := range pgContainers() {
		out[i] = runPSQL(t, c, `SELECT count(*) FROM config WHERE key = '`+key+`'`) != "0"
	}

	return out
}

// deleteConfigs deletes the config rows on the selected sides.
func deleteConfigs(t *testing.T, sides [2]bool, keys ...string) {
	t.Helper()

	sql := `DELETE FROM config WHERE key IN ('` + strings.Join(keys, `', '`) + `')`

	for i, c := range pgContainers() {
		if sides[i] {
			runPSQL(t, c, sql)
		}
	}
}

func rawJSON(body string) option {
	return withBody("application/json", []byte(body))
}

func TestConfigPublicGet(t *testing.T) {
	t.Parallel()

	keys := []string{
		"ai.enabled", "ai.quota.chat", "practice.trialSize", "ai.ratePerMinute", "ai.model", "app.profile",
		"consent.privacyPolicyVersion", "unknown.key", "%E4%B8%AD",
	}

	for _, lang := range []string{"en", "zh"} {
		for _, key := range keys {
			t.Run(lang+"/"+key, func(t *testing.T) {
				t.Parallel()

				requireSameBody(t, do(t, http.MethodGet, _configURL+"/"+key, withHeader("Accept-Language", lang)))
			})
		}
	}
}

func TestConfigAdminGet(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")

	for _, key := range []string{"ai.ratePerMinute", "ai.enabled", "practice.dailySetSize", "ai.model", "unknown.key"} {
		requireSameBody(t, do(t, http.MethodGet, _configURL+"/admin/"+key, withTokens(tokens)))
	}
}

func TestConfigRequiresPermission(t *testing.T) {
	t.Parallel()

	john := loginBoth(t, "john.doe@example.com")

	requireSameBody(t, do(t, http.MethodGet, _configURL+"/admin/ai.enabled", withTokens(john)))
	requireSameBody(t, do(t, http.MethodPut, _configURL+"/parity.denied", withTokens(john), rawJSON(`{"value":{}}`)))
	requireSameBody(t, do(t, http.MethodGet, _configURL+"/admin/ai.enabled"))
	requireSameBody(t, do(t, http.MethodPut, _configURL+"/parity.denied", rawJSON(`{"value":{}}`)))
}

func TestConfigUpsertErrors(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")
	key := "parity." + _run

	t.Cleanup(func() { deleteConfigs(t, [2]bool{true, true}, key) })

	cases := [][2]string{
		{"app.profile", `{"value":{}}`},
		{key, `{}`},
		{key, `{"value":"x","isPublic":"yes"}`},
		{key, `{"value":[1],"isPublic":null}`},
		{key, `{"value":null}`},
	}

	for _, c := range cases {
		requireSameBody(t, do(t, http.MethodPut, _configURL+"/"+c[0], withTokens(tokens), rawJSON(c[1])))
	}
}

func TestConfigUpsertLifecycle(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")
	key := "parity.lifecycle." + _run
	url := _configURL + "/" + key
	admin := withTokens(tokens)

	t.Cleanup(func() { deleteConfigs(t, [2]bool{true, true}, key) })

	requireSameBody(t, do(t, http.MethodGet, url))

	created := do(t, http.MethodPut, url, admin, rawJSON(`{"value":{"b":1,"a":{"y":2,"x":1}}}`))
	requireSameBody(t, created)
	assert.Equal(t, false, dataOf(t, created.Go)["isPublic"])

	requireSameBody(t, do(t, http.MethodGet, url))
	requireSameBody(t, do(t, http.MethodGet, _configURL+"/admin/"+key, admin))

	same := do(t, http.MethodPut, url, admin, rawJSON(`{"value":{"a":{"x":1,"y":2},"b":1}}`))
	requireSameBody(t, same)

	for _, r := range []*response{same.Go, same.Nestjs} {
		data := dataOf(t, r)
		assert.Equal(t, data["createdAt"], data["updatedAt"])
	}

	requireSameBody(t, do(t, http.MethodPut, url, admin, rawJSON(`{"value":{"c":true},"isPublic":true}`)))
	requireSameBody(t, do(t, http.MethodGet, url))

	kept := do(t, http.MethodPut, url, admin, rawJSON(`{"value":{"c":false}}`))
	assert.Equal(t, true, dataOf(t, kept.Go)["isPublic"])
	requireSameBody(t, kept)
	requireSameBody(t, do(t, http.MethodGet, url))
}

//nolint:paralleltest // writes a seeded config row that other tests read
func TestConfigUpsertRegisteredKeyRestoresOriginal(t *testing.T) {
	tokens := loginBoth(t, "admin@example.com")
	key := "ai.ratePerMinute"
	admin := withTokens(tokens)
	existed := configExists(t, key)

	original := do(t, http.MethodGet, _configURL+"/admin/"+key, admin)
	values := [2]any{dataOf(t, original.Go)["value"], dataOf(t, original.Nestjs)["value"]}

	t.Cleanup(func() {
		for i, base := range []string{baseURL("PARITY_GO_URL", "http://localhost:3000"), baseURL("PARITY_NESTJS_URL", "http://localhost:3001")} {
			raw, err := json.Marshal(map[string]any{"value": values[i]})
			require.NoError(t, err)

			opts := &requestOptions{headers: map[string]string{"Content-Type": "application/json"}, body: raw, sides: [2]side{}}
			r := send(t, base, http.MethodPut, _configURL+"/"+key, opts, side{headers: map[string]string{"Authorization": "Bearer " + tokens[i]}, target: ""})
			require.Equal(t, http.StatusOK, r.Status, string(r.Body))
		}

		deleteConfigs(t, [2]bool{!existed[0], !existed[1]}, key)
	})

	requireSameBody(t, do(t, http.MethodPut, _configURL+"/"+key, admin, rawJSON(`{"value":{"value":11},"isPublic":true}`)))
	requireSameBody(t, do(t, http.MethodGet, _configURL+"/"+key))
	requireSameBody(t, do(t, http.MethodGet, _configURL+"/admin/"+key, admin))
}
