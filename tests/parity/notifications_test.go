//go:build parity

package parity_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

const _notificationsURL = "/api/v1/notifications"

func notificationVolatileKeys() map[string]bool {
	return map[string]bool{"createdAt": true, "updatedAt": true, "readAt": true, "deletedAt": true, "sentAt": true}
}

// normalizeNotification hides timestamps and numeric ids and user ids.
func normalizeNotification(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			out[k] = normalizeNotification(item, k)
		}

		return out
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = normalizeNotification(item, "")
		}

		return out
	}

	if notificationVolatileKeys()[key] && v != nil {
		return "<time>"
	}

	if _, isNumber := v.(float64); isNumber && (key == "id" || key == "userId") {
		return "<id>"
	}

	return v
}

func requireSameNotificationBody(t *testing.T, p pair) map[string]any {
	t.Helper()

	require.Equal(t, p.Nestjs.Status, p.Go.Status, "go: %s\nnestjs: %s", p.Go.Body, p.Nestjs.Body)
	require.Equal(t, normalizeNotification(p.Nestjs.JSON(t), ""), normalizeNotification(p.Go.JSON(t), ""))

	body, _ := p.Go.JSON(t).(map[string]any)

	return body
}

func TestNotificationList(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "notify.list")

	for _, query := range []string{
		"", "?page=2&limit=5", "?limit=500", "?page=0&limit=", "?category=ORDER&isRead=false",
		"?page=-1&limit=abc&category=NOPE&isRead=x", "?page=1.5",
	} {
		t.Run(query, func(t *testing.T) {
			t.Parallel()

			requireSameNotificationBody(t, do(t, http.MethodGet, _notificationsURL+query, withTokens(tokens)))
		})
	}
}

func TestNotificationSingleOperations(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "notify.single")

	for _, route := range [][2]string{
		{"GET", "/unread-count"},
		{"GET", "/999999"},
		{"GET", "/abc"},
		{"GET", "/0"},
		{"DELETE", "/999999"},
		{"DELETE", "/2147483648"},
		{"POST", "/read-all"},
		{"POST", "/999999/read"},
		{"POST", "/x/read"},
	} {
		t.Run(route[0]+" "+route[1], func(t *testing.T) {
			t.Parallel()

			requireSameNotificationBody(t, do(t, route[0], _notificationsURL+route[1], withTokens(tokens), withHeader("Accept-Language", "zh")))
		})
	}
}

func TestNotificationSettingsLifecycle(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "notify.settings")
	url := _notificationsURL + "/settings"

	listed := requireSameNotificationBody(t, do(t, http.MethodGet, url, withTokens(tokens)))
	items, _ := listed["data"].([]any)

	categories := make([]string, 0, len(items))

	for _, item := range items {
		setting, _ := item.(map[string]any)
		category, _ := setting["category"].(string)
		categories = append(categories, category)
	}

	require.ElementsMatch(t, []string{"ORDER", "PAYMENT", "SYSTEM", "PROMOTION", "ACTIVITY", "STUDY"}, categories)

	body := map[string]any{"category": "ORDER", "enableInApp": true, "enableEmail": true, "enableSms": true, "enablePush": true}

	unchanged := do(t, http.MethodPut, url, withTokens(tokens), withJSON(t, body))
	requireSameNotificationBody(t, unchanged)

	for _, r := range []*response{unchanged.Go, unchanged.Nestjs} {
		data := dataOf(t, r)
		require.Equal(t, data["createdAt"], data["updatedAt"])
	}

	body["enableSms"] = false
	changed := do(t, http.MethodPut, url, withTokens(tokens), withJSON(t, body))
	requireSameNotificationBody(t, changed)

	for _, r := range []*response{changed.Go, changed.Nestjs} {
		data := dataOf(t, r)
		require.NotEqual(t, data["createdAt"], data["updatedAt"])
	}
}

func TestNotificationSettingsValidation(t *testing.T) {
	t.Parallel()

	tokens := tempUser(t, "notify.validation")

	for i, body := range []map[string]any{
		{},
		{"category": "NOPE", "enableInApp": "yes", "enableEmail": 1},
		{"category": "ORDER", "enableInApp": true, "enableEmail": true, "enableSms": true},
	} {
		t.Run("body "+strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			requireSameNotificationBody(t, do(t, http.MethodPut, _notificationsURL+"/settings", withTokens(tokens), withJSON(t, body)))
		})
	}
}

func TestNotificationsUnauthenticated(t *testing.T) {
	t.Parallel()

	for _, route := range [][2]string{{"GET", ""}, {"GET", "/unread-count"}, {"PUT", "/settings"}, {"DELETE", "/abc"}} {
		t.Run(route[0]+" "+route[1], func(t *testing.T) {
			t.Parallel()

			requireSameNotificationBody(t, do(t, route[0], _notificationsURL+route[1]))
		})
	}
}

// listItemKeys returns the keys of every data.list item in document order.
func listItemKeys(t *testing.T, r *response) [][]string {
	t.Helper()

	var body struct {
		Data struct {
			List []json.RawMessage `json:"list"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(r.Body, &body), string(r.Body))

	out := make([][]string, len(body.Data.List))

	for i, item := range body.Data.List {
		dec := json.NewDecoder(bytes.NewReader(item))
		_, err := dec.Token()
		require.NoError(t, err)

		for dec.More() {
			tok, err := dec.Token()
			require.NoError(t, err)

			key, _ := tok.(string)
			out[i] = append(out[i], key)

			var skip json.RawMessage
			require.NoError(t, dec.Decode(&skip))
		}
	}

	return out
}

func TestAdminNotificationListShape(t *testing.T) {
	t.Parallel()

	p := do(t, http.MethodGet, _notificationsURL+"?limit=50", withTokens(loginBoth(t, "admin@example.com")))
	require.Equal(t, http.StatusOK, p.Go.Status, string(p.Go.Body))
	require.Equal(t, http.StatusOK, p.Nestjs.Status, string(p.Nestjs.Body))

	all := []string{
		"id", "userId", "type", "category", "title", "content", "metadata", "channels", "sentChannels", "isRead",
		"readAt", "createdAt", "updatedAt", "deletedAt",
	}

	for _, r := range []*response{p.Go, p.Nestjs} {
		for _, keys := range listItemKeys(t, r) {
			present := map[string]bool{}
			for _, k := range keys {
				present[k] = true
			}

			var expected []string

			for _, k := range all {
				if (k != "metadata" && k != "readAt") || present[k] {
					expected = append(expected, k)
				}
			}

			require.Equal(t, expected, keys)
		}
	}
}

func TestAdminRetriggerCleanupErrors(t *testing.T) {
	t.Parallel()

	tokens := loginBoth(t, "admin@example.com")

	for _, id := range []string{"2", "999999", "abc", "0"} {
		for _, lang := range []string{"en", "zh"} {
			t.Run(id+"/"+lang, func(t *testing.T) {
				t.Parallel()

				requireSameBody(t, do(t, http.MethodPost, "/api/v1/admin/users/"+id+"/cleanup", withTokens(tokens), withHeader("Accept-Language", lang)))
			})
		}
	}
}
