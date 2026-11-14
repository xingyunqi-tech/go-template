//go:build integration

package app_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/usecase/notification"
)

const _notificationsURL = "/api/v1/notifications"

type notificationRow struct {
	title, category string
	channels        string
	metadata        *string
	read, deleted   bool
	ageSeconds      int
}

func (a *testApp) userID(email string) int {
	a.t.Helper()

	var id int
	require.NoError(a.t, a.pool.QueryRow(a.t.Context(), `SELECT "id" FROM "user" WHERE "email" = $1`, email).Scan(&id))

	return id
}

func (a *testApp) insertNotification(owner int, n notificationRow) int {
	a.t.Helper()

	if n.category == "" {
		n.category = "SYSTEM"
	}

	if n.channels == "" {
		n.channels = `["IN_APP"]`
	}

	var id int
	require.NoError(a.t, a.pool.QueryRow(a.t.Context(), `INSERT INTO "notification"
		("userId", "type", "category", "title", "content", "metadata", "channels", "isRead", "deletedAt", "createdAt")
		VALUES ($1, 'INFO', $2, $3, 'Body', $4::jsonb, $5::jsonb, $6, CASE WHEN $7 THEN now() END, now() - make_interval(secs => $8))
		RETURNING "id"`, owner, n.category, n.title, n.metadata, n.channels, n.read, n.deleted, n.ageSeconds).Scan(&id))

	return id
}

// objectKeys lists the keys of a JSON object in document order.
func objectKeys(t *testing.T, raw json.RawMessage) []string {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(raw))

	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok)

	var keys []string

	for dec.More() {
		tok, err := dec.Token()
		require.NoError(t, err)

		key, _ := tok.(string)
		keys = append(keys, key)

		var skip json.RawMessage
		require.NoError(t, dec.Decode(&skip))
	}

	return keys
}

type notificationPage struct {
	Data struct {
		List       []json.RawMessage `json:"list"`
		Total      int               `json:"total"`
		Page       int               `json:"page"`
		Limit      int               `json:"limit"`
		TotalPages int               `json:"totalPages"`
	} `json:"data"`
}

func listIDs(t *testing.T, r *result) []int {
	t.Helper()

	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	var page notificationPage
	require.NoError(t, json.Unmarshal([]byte(r.Raw), &page))

	ids := make([]int, len(page.Data.List))

	for i, item := range page.Data.List {
		var n struct {
			ID int `json:"id"`
		}
		require.NoError(t, json.Unmarshal(item, &n))
		ids[i] = n.ID
	}

	return ids
}

func TestNotificationsListShowsVisibleNotificationsNewestFirst(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00)
	johnID := a.userID("john.doe@example.com")
	metadata := `{"orderId": 1, "orderNo": "N1"}`

	older := a.insertNotification(johnID, notificationRow{title: "older", ageSeconds: 60})
	newer := a.insertNotification(johnID, notificationRow{title: "newer", category: "ORDER", metadata: &metadata})
	a.insertNotification(johnID, notificationRow{title: "email only", channels: `["EMAIL"]`})
	a.insertNotification(johnID, notificationRow{title: "deleted", deleted: true})
	a.insertNotification(a.userID("admin@example.com"), notificationRow{title: "someone else"})

	r := a.do(http.MethodGet, _notificationsURL, john.access, nil)
	assert.Equal(t, []int{newer, older}, listIDs(t, r))

	var page notificationPage
	require.NoError(t, json.Unmarshal([]byte(r.Raw), &page))
	assert.Equal(t, []int{2, 1, 10, 1}, []int{page.Data.Total, page.Data.Page, page.Data.Limit, page.Data.TotalPages})
	assert.Equal(t, []string{
		"id", "userId", "type", "category", "title", "content", "metadata", "channels", "sentChannels", "isRead",
		"createdAt", "updatedAt", "deletedAt",
	}, objectKeys(t, page.Data.List[0]))
	assert.NotContains(t, objectKeys(t, page.Data.List[1]), "metadata")
	assert.Contains(t, string(page.Data.List[0]), `"metadata":{"orderId":1,"orderNo":"N1"},"channels":["IN_APP"],"sentChannels":{},"isRead":false`)
	assert.Contains(t, string(page.Data.List[0]), `"deletedAt":null}`)
}

func TestNotificationsListFiltersAndPaginates(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00)
	johnID := a.userID("john.doe@example.com")

	order := a.insertNotification(johnID, notificationRow{title: "order", category: "ORDER", ageSeconds: 1})
	read := a.insertNotification(johnID, notificationRow{title: "read", read: true, ageSeconds: 2})
	a.insertNotification(johnID, notificationRow{title: "plain", ageSeconds: 3})

	assert.Equal(t, []int{order}, listIDs(t, a.do(http.MethodGet, _notificationsURL+"?category=ORDER", john.access, nil)))
	assert.Equal(t, []int{read}, listIDs(t, a.do(http.MethodGet, _notificationsURL+"?isRead=true", john.access, nil)))
	assert.Len(t, listIDs(t, a.do(http.MethodGet, _notificationsURL+"?isRead=false", john.access, nil)), 2)

	capped := a.do(http.MethodGet, _notificationsURL+"?limit=500", john.access, nil)
	assert.Contains(t, capped.Raw, `"total":3,"page":1,"limit":50,"totalPages":1}`)

	second := a.do(http.MethodGet, _notificationsURL+"?page=2&limit=2", john.access, nil)
	assert.Len(t, listIDs(t, second), 1)
	assert.Contains(t, second.Raw, `"total":3,"page":2,"limit":2,"totalPages":2}`)

	empty := a.do(http.MethodGet, _notificationsURL+"?limit=", john.access, nil)
	assert.Contains(t, empty.Raw, `"limit":10,`)
}

func TestNotificationsListRejectsInvalidQuery(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00)

	r := a.do(http.MethodGet, _notificationsURL+"?page=-1&category=NOPE&isRead=x", john.access, nil)
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{
		"page":"page must not be less than 1",
		"category":"category must be one of the following values: ORDER, PAYMENT, SYSTEM, PROMOTION, ACTIVITY, STUDY",
		"isRead":"isRead must be a boolean value"}}`, r.Raw)

	r = a.do(http.MethodGet, _notificationsURL+"?page=1.5", john.access, nil)
	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"page":"page must be an integer number"}}`, r.Raw)

	assert.JSONEq(t, `{"code":401,"message":"Unauthorized","data":null}`, a.do(http.MethodGet, _notificationsURL, "", nil).Raw)
}

func TestNotificationReadAndDelete(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00)
	johnID := a.userID("john.doe@example.com")

	own := a.insertNotification(johnID, notificationRow{title: "own"})
	second := a.insertNotification(johnID, notificationRow{title: "second"})
	a.insertNotification(johnID, notificationRow{title: "read", read: true})
	a.insertNotification(johnID, notificationRow{title: "email", channels: `["EMAIL"]`})
	foreign := a.insertNotification(a.userID("admin@example.com"), notificationRow{title: "foreign"})
	path := func(id int, suffix string) string { return _notificationsURL + "/" + strconv.Itoa(id) + suffix }

	assert.JSONEq(t, `{"code":200,"message":"success","data":2}`, a.do(http.MethodGet, _notificationsURL+"/unread-count", john.access, nil).Raw)

	got := a.do(http.MethodGet, path(own, ""), john.access, nil)
	assert.InDelta(t, own, got.data()["id"], 0)

	notFound := `{"code":404,"message":"Notification not found","data":null}`
	assert.JSONEq(t, notFound, a.do(http.MethodGet, path(foreign, ""), john.access, nil, "Accept-Language", "zh").Raw)
	assert.JSONEq(t, `{"code":400,"message":"Bad request","data":{"id":"Invalid id"}}`,
		a.do(http.MethodGet, _notificationsURL+"/abc", john.access, nil).Raw)

	read := a.do(http.MethodPost, path(own, "/read"), john.access, nil)
	require.Equal(t, http.StatusOK, read.Status, read.Raw)
	assert.Equal(t, true, read.data()["isRead"])
	assert.Regexp(t, `"isRead":true,"readAt":"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z","createdAt"`, read.Raw)
	assert.JSONEq(t, notFound, a.do(http.MethodPost, path(999_999, "/read"), john.access, nil).Raw)
	assert.JSONEq(t, notFound, a.do(http.MethodPost, path(foreign, "/read"), john.access, nil).Raw)

	assert.JSONEq(t, `{"code":200,"message":"success","data":1}`, a.do(http.MethodGet, _notificationsURL+"/unread-count", john.access, nil).Raw)
	assert.JSONEq(t, `{"code":200,"message":"success"}`, a.do(http.MethodPost, _notificationsURL+"/read-all", john.access, nil).Raw)
	assert.JSONEq(t, `{"code":200,"message":"success","data":0}`, a.do(http.MethodGet, _notificationsURL+"/unread-count", john.access, nil).Raw)

	var readAt *time.Time
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "readAt" FROM "notification" WHERE "id" = $1`, second).Scan(&readAt))
	assert.NotNil(t, readAt)

	assert.JSONEq(t, `{"code":200,"message":"success"}`, a.do(http.MethodDelete, path(own, ""), john.access, nil).Raw)
	assert.JSONEq(t, notFound, a.do(http.MethodDelete, path(own, ""), john.access, nil).Raw)
	assert.JSONEq(t, notFound, a.do(http.MethodDelete, path(foreign, ""), john.access, nil).Raw)

	var deleted bool
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "deletedAt" IS NOT NULL FROM "notification" WHERE "id" = $1`, own).Scan(&deleted))
	assert.True(t, deleted)
}

func TestNotificationSettings(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00)
	body := map[string]any{"category": "ORDER", "enableInApp": true, "enableEmail": false, "enableSms": false, "enablePush": true}
	url := _notificationsURL + "/settings"

	created := a.do(http.MethodPut, url, john.access, body)
	require.Equal(t, http.StatusOK, created.Status, created.Raw)

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(created.Raw), &envelope))
	assert.Equal(t, []string{
		"id", "userId", "category", "enableInApp", "enableEmail", "enableSms", "enablePush", "createdAt", "updatedAt",
	}, objectKeys(t, envelope.Data))
	assert.InDelta(t, a.userID("john.doe@example.com"), created.data()["userId"], 0)

	unchanged := a.do(http.MethodPut, url, john.access, body)
	assert.Equal(t, created.data(), unchanged.data())

	body["enableEmail"] = true
	updated := a.do(http.MethodPut, url, john.access, body)
	assert.Equal(t, created.data()["id"], updated.data()["id"])
	assert.Equal(t, true, updated.data()["enableEmail"])
	assert.NotEqual(t, created.data()["updatedAt"], updated.data()["updatedAt"])

	listed := a.do(http.MethodGet, url, john.access, nil)
	settings, _ := listed.Body["data"].([]any)
	require.Len(t, settings, 1)

	invalid := a.do(http.MethodPut, url, john.access, map[string]any{"category": "X"})
	assert.Equal(t, http.StatusUnprocessableEntity, invalid.Status)
	assert.Len(t, invalid.data(), 5)
}

func TestRegistrationCreatesDefaultNotificationSettings(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	r := a.do(http.MethodPost, "/api/v1/auth/email/register", "", map[string]any{
		"email": "settings@example.com", "password": _secret00, "firstName": "S", "lastName": "U",
	})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	listed := a.do(http.MethodGet, _notificationsURL+"/settings", a.login("settings@example.com", _secret00).access, nil)
	settings, _ := listed.Body["data"].([]any)

	var categories []string

	for _, s := range settings {
		setting, _ := s.(map[string]any)
		category, _ := setting["category"].(string)
		categories = append(categories, category)

		assert.Equal(t, []any{true, true, true, true},
			[]any{setting["enableInApp"], setting["enableEmail"], setting["enableSms"], setting["enablePush"]})
	}

	assert.Equal(t, []string{"ORDER", "PAYMENT", "SYSTEM", "PROMOTION", "ACTIVITY", "STUDY"}, categories)
}

func newNotifications(t *testing.T, a *testApp) *notification.Service {
	t.Helper()

	log := slog.New(slog.DiscardHandler)

	inserter, err := river.NewClient(riverpgxv5.New(a.pool), postgres.RiverConfig(log))
	require.NoError(t, err)

	return notification.New(postgres.NewNotifications(postgres.NewDB(a.pool, time.Second, log)), postgres.NewJobs(inserter), log)
}

func TestCreatedNotificationQueuesAllowedChannelsAndWorkerRecordsDelivery(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	johnID := a.userID("john.doe@example.com")
	_, err := a.pool.Exec(t.Context(), `INSERT INTO "notification_setting" ("userId", "category", "enableSms") VALUES ($1, 'ORDER', false)`, johnID)
	require.NoError(t, err)
	_, err = a.pool.Exec(t.Context(), `UPDATE "user" SET "phone" = '13800000000' WHERE "id" = $1`, johnID)
	require.NoError(t, err)

	service := newNotifications(t, a)

	n, err := service.Create(t.Context(), &notification.Input{
		UserID: johnID, Type: "INFO", Category: "ORDER", Title: "Shipped", Content: "Your order shipped",
		Channels: []string{"IN_APP", "EMAIL", "SMS"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"IN_APP", "EMAIL"}, n.Channels)
	assert.Nil(t, n.Metadata)

	paid, err := service.Create(t.Context(), &notification.Input{
		UserID: johnID, Type: "SUCCESS", Category: "PAYMENT", Title: "Paid", Content: "NO1 paid", Channels: []string{"SMS", "PUSH"},
		Metadata: map[string]any{"wechatSubscribe": map[string]any{"scene": "orderPaid", "values": map[string]any{"orderNo": "NO1"}}},
	})
	require.NoError(t, err)

	email := jobs(t, a, "send-email")
	require.Len(t, email, 1)
	assert.Equal(t, "notification", email[0].Queue)
	assert.Equal(t, 3, email[0].MaxAttempts)
	assert.Equal(t, map[string]any{"notificationId": float64(n.ID), "userId": float64(johnID), "title": "Shipped", "content": "Your order shipped"}, email[0].Args)

	sms := jobs(t, a, "send-sms")
	require.Len(t, sms, 1)
	assert.Equal(t, map[string]any{"notificationId": float64(paid.ID), "userId": float64(johnID), "content": "NO1 paid"}, sms[0].Args)

	subscribe := jobs(t, a, "send-wechat-subscribe")
	require.Len(t, subscribe, 1)
	assert.Equal(t, map[string]any{"scene": "orderPaid", "values": map[string]any{"orderNo": "NO1"}}, subscribe[0].Args["payload"])

	sent := &captured{}
	startWorker(t, a, sent)

	sentChannels := func(id int) map[string]map[string]any {
		var raw []byte
		require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "sentChannels" FROM "notification" WHERE "id" = $1`, id).Scan(&raw))

		out := map[string]map[string]any{}
		require.NoError(t, json.Unmarshal(raw, &out))

		return out
	}

	require.Eventually(t, func() bool { return len(sentChannels(n.ID)) > 0 && len(sentChannels(paid.ID)) > 0 }, _waitFor, 50*time.Millisecond)

	assert.Equal(t, true, sentChannels(n.ID)["EMAIL"]["sent"])
	assert.Regexp(t, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`, sentChannels(n.ID)["EMAIL"]["sentAt"])
	require.Len(t, sent.all(), 1)
	assert.Equal(t, "john.doe@example.com", sent.all()[0].To)
	assert.Equal(t, "<p>Your order shipped</p>", sent.all()[0].HTML)

	assert.Equal(t, map[string]any{"sent": false, "error": "SMS client not initialized"},
		map[string]any{"sent": sentChannels(paid.ID)["SMS"]["sent"], "error": sentChannels(paid.ID)["SMS"]["error"]})
	require.Eventually(t, func() bool { return jobs(t, a, "send-wechat-subscribe")[0].State == "completed" }, _waitFor, 50*time.Millisecond)
	assert.NotContains(t, sentChannels(paid.ID), "PUSH")
}

func TestInAppBatchSkipsUsersWhoTurnedInAppOff(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	johnID, adminID := a.userID("john.doe@example.com"), a.userID("admin@example.com")
	_, err := a.pool.Exec(t.Context(), `INSERT INTO "notification_setting" ("userId", "category", "enableInApp") VALUES ($1, 'SYSTEM', false)`, adminID)
	require.NoError(t, err)

	written, err := newNotifications(t, a).CreateInAppBatch(t.Context(), &repo.InAppBatch{
		UserIDs: []int{johnID, adminID}, Type: "INFO", Category: "SYSTEM", Title: "Batch", Content: "B", Metadata: nil,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, written)
}

func TestDeletedAccountLosesNotificationsAndSettings(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	john := a.login("john.doe@example.com", _secret00)
	johnID := a.userID("john.doe@example.com")

	a.insertNotification(johnID, notificationRow{title: "kept until cleanup"})
	a.insertNotification(johnID, notificationRow{title: "deleted", deleted: true})
	require.Equal(t, http.StatusOK, a.do(http.MethodPut, _notificationsURL+"/settings", john.access, map[string]any{
		"category": "ORDER", "enableInApp": true, "enableEmail": true, "enableSms": true, "enablePush": true,
	}).Status)

	r := a.do(http.MethodDelete, "/api/v1/auth/me", john.access, map[string]any{"oldPassword": _secret00})
	require.Equal(t, http.StatusOK, r.Status, r.Raw)

	startWorker(t, a, &captured{})

	require.Eventually(t, func() bool {
		var done bool

		_ = a.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM "account_cleanup" WHERE "userId" = $1)`, johnID).Scan(&done)

		return done
	}, _waitFor, 50*time.Millisecond)

	var notifications, settings int
	require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM "notification" WHERE "userId" = $1), (SELECT count(*) FROM "notification_setting" WHERE "userId" = $1)`,
		johnID).Scan(&notifications, &settings))
	assert.Equal(t, []int{0, 0}, []int{notifications, settings})
}

func TestAdminRetriggerAccountCleanup(t *testing.T) {
	t.Parallel()

	a := newApp(t, 0)
	admin := a.login("admin@example.com", _secret00)
	johnID := a.userID("john.doe@example.com")
	url := func(id int) string { return "/api/v1/admin/users/" + strconv.Itoa(id) + "/cleanup" }

	assert.JSONEq(t, `{"code":422,"message":"Validation failed","data":{"user":"User has not been deleted"}}`,
		a.do(http.MethodPost, url(johnID), admin.access, nil).Raw)
	assert.JSONEq(t, `{"code":404,"message":"User not found","data":null}`, a.do(http.MethodPost, url(999_999), admin.access, nil).Raw)
	assert.JSONEq(t, `{"code":400,"message":"Bad request","data":{"id":"Invalid id"}}`,
		a.do(http.MethodPost, "/api/v1/admin/users/0/cleanup", admin.access, nil).Raw)

	john := a.login("john.doe@example.com", _secret00)
	assert.Equal(t, http.StatusForbidden, a.do(http.MethodPost, url(johnID), john.access, nil).Status)

	_, err := a.pool.Exec(t.Context(), `UPDATE "user" SET "deletedAt" = now() WHERE "id" = $1`, johnID)
	require.NoError(t, err)

	assert.JSONEq(t, `{"code":200,"message":"success"}`, a.do(http.MethodPost, url(johnID), admin.access, nil).Raw)
	assert.JSONEq(t, `{"code":200,"message":"success"}`, a.do(http.MethodPost, url(johnID), admin.access, nil).Raw)

	queued := jobs(t, a, "clean-account")
	require.Len(t, queued, 1)
	assert.InDelta(t, johnID, queued[0].Args["userId"], 0)
}
