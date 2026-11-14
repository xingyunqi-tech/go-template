package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/webapi/mail"
	"github.com/xingyunqi-tech/go-template/internal/usecase/notification"
)

type usersByID struct {
	repo.Users

	users map[int]*entity.User
}

func (u usersByID) Get(_ context.Context, q repo.UserQuery) (*entity.User, error) {
	return u.users[*q.ID], nil
}

type channelResults struct {
	repo.Notifications

	mu      sync.Mutex
	results []map[string]any
}

func (c *channelResults) RecordChannel(_ context.Context, id int, channel string, result map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(result, "sentAt")
	c.results = append(c.results, map[string]any{"id": id, "channel": channel, "result": result})

	return nil
}

type mails struct {
	sent []mail.Outgoing
	err  error
}

func (m *mails) Send(_ context.Context, out *mail.Outgoing) error {
	m.sent = append(m.sent, *out)

	return m.err
}

type smsFunc func(phone string, params map[string]string) error

func (f smsFunc) SendSMS(_ context.Context, phone string, params map[string]string) error {
	return f(phone, params)
}

type subscribeFunc func(m *SubscribeMessage) error

func (f subscribeFunc) SendSubscribeMessage(_ context.Context, m *SubscribeMessage) error {
	return f(m)
}

func strPtr(s string) *string { return &s }

func testNotifier(results *channelResults, sender mail.Sender, mutate func(*NotificationDeps)) *notifier {
	log := slog.New(slog.DiscardHandler)

	deps := NotificationDeps{
		Notifications: notification.New(results, nil, log),
		Users: usersByID{users: map[int]*entity.User{
			1: {ID: 1, Email: strPtr("a@example.com"), Phone: strPtr("13800000000"), WechatOpenID: strPtr("openid")},
			2: {ID: 2},
		}},
		SMS: nil, Subscribe: nil, Templates: nil,
	}
	if mutate != nil {
		mutate(&deps)
	}

	return newNotifier(&deps, sender, log)
}

func TestEmailJobSendsAndRecordsTheResult(t *testing.T) {
	t.Parallel()

	results := &channelResults{}
	sent := &mails{}
	n := testNotifier(results, sent, nil)

	require.NoError(t, n.email(t.Context(), &entity.NotificationEmailJob{NotificationID: 7, UserID: 1, Title: "T", Content: "C"}))
	assert.Equal(t, []mail.Outgoing{{To: "a@example.com", Subject: "T", Text: "C", HTML: "<p>C</p>"}}, sent.sent)

	err := n.email(t.Context(), &entity.NotificationEmailJob{NotificationID: 8, UserID: 2, Title: "T", Content: "C"})
	require.EqualError(t, err, "User 2 not found or has no email")

	assert.Equal(t, []map[string]any{
		{"id": 7, "channel": "EMAIL", "result": map[string]any{"sent": true}},
		{"id": 8, "channel": "EMAIL", "result": map[string]any{"sent": false, "error": "User 2 not found or has no email"}},
	}, results.results)
}

func TestSMSJobFailsWithoutAnSMSClient(t *testing.T) {
	t.Parallel()

	results := &channelResults{}
	err := testNotifier(results, &mails{}, nil).sms(t.Context(), &entity.NotificationSMSJob{NotificationID: 7, UserID: 1, Content: "C"})
	require.EqualError(t, err, "SMS client not initialized")

	var got map[string]string

	n := testNotifier(results, &mails{}, func(d *NotificationDeps) {
		d.SMS = smsFunc(func(phone string, params map[string]string) error {
			got = map[string]string{"phone": phone, "content": params["content"]}

			return nil
		})
	})
	require.NoError(t, n.sms(t.Context(), &entity.NotificationSMSJob{NotificationID: 8, UserID: 1, Content: "C"}))
	assert.Equal(t, map[string]string{"phone": "13800000000", "content": "C"}, got)

	assert.Equal(t, []map[string]any{
		{"id": 7, "channel": "SMS", "result": map[string]any{"sent": false, "error": "SMS client not initialized"}},
		{"id": 8, "channel": "SMS", "result": map[string]any{"sent": true}},
	}, results.results)
}

func orderPaid() map[string]SubscribeTemplate {
	return map[string]SubscribeTemplate{
		"orderPaid": {TemplateID: "tpl", Fields: map[string]string{"orderNo": "character_string1", "amount": "amount2", "title": "thing3"}},
	}
}

func orderPaidPayload(t *testing.T) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(map[string]any{
		"scene": "orderPaid", "page": "pages/order",
		"values": map[string]any{"orderNo": "N1", "amount": "9.90元", "title": "一二三四五六七八九十一二三四五六七八九十超出"},
	})
	require.NoError(t, err)

	return raw
}

func TestBuildSubscribeMessageMapsAndTruncatesFields(t *testing.T) {
	t.Parallel()

	msg, scene, ok := BuildSubscribeMessage(orderPaidPayload(t), orderPaid())
	require.True(t, ok)
	assert.Equal(t, "orderPaid", scene)
	assert.Equal(t, &SubscribeMessage{OpenID: "", TemplateID: "tpl", Page: "pages/order", Data: map[string]string{
		"character_string1": "N1", "amount2": "9.90元", "thing3": "一二三四五六七八九十一二三四五六七八九十",
	}}, msg)

	_, _, ok = BuildSubscribeMessage(orderPaidPayload(t), nil)
	assert.False(t, ok)
	assert.Equal(t, "abcde", FormatSubscribeValue("phrase1", "abcdefg"))
	assert.Equal(t, "abcdefg", FormatSubscribeValue("time1", "abcdefg"))
}

func TestWechatSubscribeJob(t *testing.T) {
	t.Parallel()

	payload := orderPaidPayload(t)

	t.Run("skips a scene without template", func(t *testing.T) {
		t.Parallel()

		results := &channelResults{}
		require.NoError(t, testNotifier(results, &mails{}, nil).wechatSubscribe(t.Context(),
			&entity.NotificationWechatSubscribeJob{NotificationID: 7, UserID: 1, Payload: payload}))
		assert.Empty(t, results.results)
	})

	t.Run("records a user without openid", func(t *testing.T) {
		t.Parallel()

		results := &channelResults{}
		n := testNotifier(results, &mails{}, func(d *NotificationDeps) { d.Templates = orderPaid() })
		require.NoError(t, n.wechatSubscribe(t.Context(), &entity.NotificationWechatSubscribeJob{NotificationID: 7, UserID: 2, Payload: payload}))
		assert.Equal(t, []map[string]any{
			{"id": 7, "channel": "PUSH", "result": map[string]any{"sent": false, "error": "User has no WeChat openId"}},
		}, results.results)
	})

	t.Run("cancels unrecoverable failures and retries others", func(t *testing.T) {
		t.Parallel()

		var calls int

		results := &channelResults{}
		n := testNotifier(results, &mails{}, func(d *NotificationDeps) {
			d.Templates = orderPaid()
			d.Subscribe = subscribeFunc(func(m *SubscribeMessage) error {
				calls++

				switch calls {
				case 1:
					return fmt.Errorf("%w: 43101 user refuse to accept the msg", ErrUnrecoverable)
				case 2:
					return errors.New("system busy")
				default:
					assert.Equal(t, "openid", m.OpenID)

					return nil
				}
			})
		})

		job := &entity.NotificationWechatSubscribeJob{NotificationID: 7, UserID: 1, Payload: payload}

		var cancel *river.JobCancelError
		require.ErrorAs(t, n.wechatSubscribe(t.Context(), job), &cancel)
		require.EqualError(t, n.wechatSubscribe(t.Context(), job), "system busy")
		require.NoError(t, n.wechatSubscribe(t.Context(), job))

		assert.Equal(t, []map[string]any{
			{"id": 7, "channel": "PUSH", "result": map[string]any{"sent": false, "error": "unrecoverable: 43101 user refuse to accept the msg"}},
			{"id": 7, "channel": "PUSH", "result": map[string]any{"sent": false, "error": "system busy"}},
			{"id": 7, "channel": "PUSH", "result": map[string]any{"sent": true}},
		}, results.results)
	})

	t.Run("cancels when WeChat is not configured", func(t *testing.T) {
		t.Parallel()

		results := &channelResults{}
		n := testNotifier(results, &mails{}, func(d *NotificationDeps) { d.Templates = orderPaid() })

		var cancel *river.JobCancelError
		require.ErrorAs(t, n.wechatSubscribe(t.Context(),
			&entity.NotificationWechatSubscribeJob{NotificationID: 7, UserID: 1, Payload: payload}), &cancel)
		assert.Equal(t, []map[string]any{
			{"id": 7, "channel": "PUSH", "result": map[string]any{"sent": false, "error": "Unprocessable Entity Exception"}},
		}, results.results)
	})
}

func TestNotificationLimiterAllowsTenJobsASecond(t *testing.T) {
	t.Parallel()

	n := testNotifier(&channelResults{}, &mails{}, nil)
	assert.InDelta(t, 10, float64(n.limiter.Limit()), 0)
	assert.Equal(t, 10, n.limiter.Burst())
}
