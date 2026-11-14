//go:build parity

package parity_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	_appProfileURL      = "/api/v1/app-profile"
	_adminAppProfileURL = "/api/v1/admin/app-profile"
)

func TestAppProfileGet(t *testing.T) {
	t.Parallel()

	requireSameBody(t, do(t, http.MethodGet, _appProfileURL))
}

func TestAppProfileUpdateValidation(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")
	bodies := []map[string]any{
		{"servicePhone": "call me", "serviceEmail": "bad", "appName": strings.Repeat("x", 21)},
		{"appName": 1, "slogan": []string{"a"}, "shareTitle": map[string]any{"a": 1}},
		{"servicePhone": "   -  ", "serviceEmail": "  a@b.co ", "icpLicense": strings.Repeat("x", 51)},
		{"copyright": strings.Repeat("c", 101), "serviceHours": strings.Repeat("h", 41), "companyName": strings.Repeat("n", 61)},
	}

	for i, body := range bodies {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			requireSameBody(t, do(t, http.MethodPut, _adminAppProfileURL, withTokens(tokens), withJSON(t, body)))
		})
	}
}

func TestAppProfileUpdatePermissions(t *testing.T) {
	t.Parallel()

	requireSameBody(t, do(t, http.MethodPut, _adminAppProfileURL, withTokens(loginBoth(t, "john.doe@example.com")), rawJSON(`{}`)))
	requireSameBody(t, do(t, http.MethodPut, _adminAppProfileURL, rawJSON(`{}`)))
}

//nolint:paralleltest // replaces the shared app.profile config row
func TestAppProfileUpdateAndRestore(t *testing.T) {
	tokens := loginBoth(t, "admin@example.com")
	admin := withTokens(tokens)
	existed := configExists(t, "app.profile")

	original := do(t, http.MethodGet, _appProfileURL)
	profiles := [2]map[string]any{dataOf(t, original.Go), dataOf(t, original.Nestjs)}

	t.Cleanup(func() {
		for i, base := range []string{baseURL("PARITY_GO_URL", "http://localhost:3000"), baseURL("PARITY_NESTJS_URL", "http://localhost:3001")} {
			raw, err := json.Marshal(profiles[i])
			require.NoError(t, err)

			opts := &requestOptions{headers: map[string]string{"Content-Type": "application/json"}, body: raw, sides: [2]side{}}
			r := send(t, base, http.MethodPut, _adminAppProfileURL, opts, side{headers: map[string]string{"Authorization": "Bearer " + tokens[i]}, target: ""})
			require.Equal(t, http.StatusOK, r.Status, string(r.Body))
		}

		deleteConfigs(t, [2]bool{!existed[0], !existed[1]}, "app.profile")
	})

	requireSameBody(t, do(t, http.MethodPut, _adminAppProfileURL, admin, withJSON(t, map[string]any{
		"appName": "  备考  ", "slogan": "   ", "shareTitle": nil, "servicePhone": " +86 400-123 4567 ",
		"serviceEmail": "help@example.com", "unknown": "x",
	})))
	requireSameBody(t, do(t, http.MethodGet, _appProfileURL))
	requireSameBody(t, do(t, http.MethodPut, _adminAppProfileURL, admin, withJSON(t, map[string]any{"slogan": "新"})))
	requireSameBody(t, do(t, http.MethodGet, _appProfileURL))
}
