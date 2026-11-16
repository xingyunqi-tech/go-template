//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/usecase/notification"
)

type deliveries struct {
	mu        sync.Mutex
	sms       []url.Values
	subscribe []map[string]any
}

func (d *deliveries) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{$}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.sms = append(d.sms, r.URL.Query())
		d.mu.Unlock()

		writeJSON(w, map[string]string{"Code": "OK", "Message": "OK", "BizId": "biz"})
	})
	mux.HandleFunc("/cgi-bin/stable_token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{"access_token": "mini-token", "expires_in": 7200})
	})
	mux.HandleFunc("/cgi-bin/message/subscribe/send", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any

		_ = json.NewDecoder(r.Body).Decode(&body)

		d.mu.Lock()
		d.subscribe = append(d.subscribe, body)
		d.mu.Unlock()

		if body["touser"] == "openid-refused" {
			writeJSON(w, map[string]any{"errcode": 43101, "errmsg": "user refuse to accept the msg"})

			return
		}

		writeJSON(w, map[string]any{"errcode": 0, "errmsg": "ok"})
	})

	return mux
}

func (d *deliveries) sent() (sms []url.Values, subscribe []map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]url.Values(nil), d.sms...), append([]map[string]any(nil), d.subscribe...)
}

func startDeliveryWorker(t *testing.T, cfg *config.Config, target *url.URL) {
	t.Helper()

	infra, err := app.OpenInfra(t.Context(), cfg, 5)
	require.NoError(t, err)
	t.Cleanup(infra.Close)

	infra.Outbound = redirect{target: target}

	client, err := app.NewWorkerClient(cfg, slog.New(slog.DiscardHandler), infra, &captured{})
	require.NoError(t, err)
	require.NoError(t, client.Start(context.WithoutCancel(t.Context())))

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_ = client.Stop(ctx)
	})
}

func TestWorkerDeliversSMSAndWechatSubscribeMessages(t *testing.T) {
	t.Parallel()

	fake := &deliveries{}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)

	target, err := url.Parse(srv.URL)
	require.NoError(t, err)

	a := newAppWith(t, 0, func(c *config.Config) {
		c.SMS.AccessKeyID, c.SMS.AccessKeySecret = "id", "secret"
		c.SMS.SignName, c.SMS.TemplateCode = "sign", "SMS_1"
		c.Wechat.MiniAppID, c.Wechat.MiniAppSecret = "mini", "mini-secret"
		c.Wechat.MiniProgramState = "trial"
		c.Wechat.OrderPaidTemplateID = "TPL1"
		c.Wechat.OrderPaidTemplateFields = config.SubscribeFields{"orderNo": "character_string1", "amount": "amount2"}
	}, nil)

	johnID := a.userID("john.doe@example.com")
	adminID := a.userID("admin@example.com")
	_, err = a.pool.Exec(t.Context(), `UPDATE "user" SET "phone" = '13800000001', "wechatOpenId" = 'openid-john' WHERE "id" = $1`, johnID)
	require.NoError(t, err)
	_, err = a.pool.Exec(t.Context(), `UPDATE "user" SET "wechatOpenId" = 'openid-refused' WHERE "id" = $1`, adminID)
	require.NoError(t, err)

	service := newNotifications(t, a)
	metadata := map[string]any{"wechatSubscribe": map[string]any{
		"scene": "orderPaid", "page": "pages/order?id=1", "values": map[string]any{"orderNo": "NO1", "amount": "9.90元"},
	}}

	paid, err := service.Create(t.Context(), &notification.Input{
		UserID: johnID, Type: "SUCCESS", Category: "PAYMENT", Title: "Paid", Content: "NO1 paid", Channels: []string{"SMS", "PUSH"},
		Metadata: metadata,
	})
	require.NoError(t, err)

	refused, err := service.Create(t.Context(), &notification.Input{
		UserID: adminID, Type: "SUCCESS", Category: "PAYMENT", Title: "Paid", Content: "NO2 paid", Channels: []string{"PUSH"},
		Metadata: metadata,
	})
	require.NoError(t, err)

	startDeliveryWorker(t, a.cfg, target)

	sentChannels := func(id int) map[string]map[string]any {
		var raw []byte
		require.NoError(t, a.pool.QueryRow(t.Context(), `SELECT "sentChannels" FROM "notification" WHERE "id" = $1`, id).Scan(&raw))

		out := map[string]map[string]any{}
		require.NoError(t, json.Unmarshal(raw, &out))

		return out
	}

	require.Eventually(t, func() bool {
		return len(sentChannels(paid.ID)) == 2 && len(sentChannels(refused.ID)) == 1
	}, _waitFor, 50*time.Millisecond)

	assert.Equal(t, true, sentChannels(paid.ID)["SMS"]["sent"])
	assert.Equal(t, true, sentChannels(paid.ID)["PUSH"]["sent"])
	assert.Equal(t, map[string]any{"sent": false, "error": "WeChat API error 43101: user refuse to accept the msg"},
		map[string]any{"sent": sentChannels(refused.ID)["PUSH"]["sent"], "error": sentChannels(refused.ID)["PUSH"]["error"]})

	sms, subscribe := fake.sent()
	require.Len(t, sms, 1)
	assert.Equal(t, "13800000001", sms[0].Get("PhoneNumbers"))
	assert.Equal(t, `{"content":"NO1 paid"}`, sms[0].Get("TemplateParam"))

	require.Len(t, subscribe, 2)

	for _, body := range subscribe {
		if body["touser"] == "openid-john" {
			assert.Equal(t, map[string]any{
				"touser": "openid-john", "template_id": "TPL1", "page": "pages/order?id=1",
				"data":              map[string]any{"character_string1": map[string]any{"value": "NO1"}, "amount2": map[string]any{"value": "9.90元"}},
				"miniprogram_state": "trial", "lang": "zh_CN",
			}, body)
		}
	}

	require.Eventually(t, func() bool {
		states := map[string]bool{}
		for _, j := range jobs(t, a, "send-wechat-subscribe") {
			states[j.State] = true
		}

		return states["completed"] && states["cancelled"] //nolint:misspell // River job state
	}, _waitFor, 50*time.Millisecond)
}
