package notification_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/notification"
)

type fakeRepo struct {
	repo.Notifications

	setting  *entity.NotificationSetting
	created  *repo.NewNotification
	updated  *repo.NotificationFlags
	inserted *repo.NotificationFlags
	recorded map[string]any
}

func (f *fakeRepo) Setting(context.Context, int, string) (*entity.NotificationSetting, error) {
	return f.setting, nil
}

func (f *fakeRepo) Create(_ context.Context, n *repo.NewNotification) (*entity.NotificationRecord, error) {
	f.created = n

	return &entity.NotificationRecord{
		ID: 9, UserID: n.UserID, Type: n.Type, Category: n.Category, Title: n.Title, Content: n.Content,
		Metadata: n.Metadata, Channels: n.Channels,
	}, nil
}

func (f *fakeRepo) CreateSetting(_ context.Context, userID int, category string, flags repo.NotificationFlags) (*entity.NotificationSetting, error) {
	f.inserted = &flags

	return &entity.NotificationSetting{ID: 1, UserID: userID, Category: category}, nil
}

func (f *fakeRepo) UpdateSetting(_ context.Context, id int, flags repo.NotificationFlags) (*entity.NotificationSetting, error) {
	f.updated = &flags

	return &entity.NotificationSetting{ID: id, EnableInApp: flags.InApp}, nil
}

func (f *fakeRepo) MarkRead(context.Context, int, int, time.Time) (*entity.NotificationRecord, error) {
	return nil, nil //nolint:nilnil // not found
}

func (f *fakeRepo) SoftDelete(context.Context, int, int) (bool, error) {
	return false, nil
}

func (f *fakeRepo) Get(context.Context, int, int) (*entity.NotificationRecord, error) {
	return nil, nil //nolint:nilnil // not found
}

func (f *fakeRepo) RecordChannel(_ context.Context, _ int, channel string, result map[string]any) error {
	f.recorded = map[string]any{channel: result}

	return nil
}

type fakeJobs struct {
	repo.Jobs

	queued []entity.NotificationJob
	err    error
}

func (f *fakeJobs) EnqueueNotification(_ context.Context, job entity.NotificationJob) error {
	f.queued = append(f.queued, job)

	return f.err
}

func newService(r *fakeRepo, j *fakeJobs) *notification.Service {
	return notification.New(r, j, slog.New(slog.DiscardHandler))
}

func TestCreateFiltersChannelsBySettingAndQueuesExternalChannels(t *testing.T) {
	t.Parallel()

	r := &fakeRepo{setting: &entity.NotificationSetting{EnableInApp: true, EnableEmail: true, EnableSMS: false, EnablePush: true}}
	j := &fakeJobs{}

	n, err := newService(r, j).Create(t.Context(), &notification.Input{
		UserID: 2, Type: "INFO", Category: "ORDER", Title: "Shipped", Content: "Body",
		Channels: []string{"IN_APP", "EMAIL", "SMS", "PUSH", "FAX"},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"IN_APP", "EMAIL", "PUSH"}, r.created.Channels)
	assert.Nil(t, r.created.Metadata)
	assert.Equal(t, []entity.NotificationJob{
		entity.NotificationEmailJob{NotificationID: n.ID, UserID: 2, Title: "Shipped", Content: "Body"},
	}, j.queued)
}

func TestCreateWithoutSettingKeepsChannelsAndQueuesSMSAndSubscribe(t *testing.T) {
	t.Parallel()

	r := &fakeRepo{}
	j := &fakeJobs{}
	subscribe := map[string]any{"scene": "orderPaid", "values": map[string]any{"orderNo": "NO1"}, "page": "pages/x"}

	n, err := newService(r, j).Create(t.Context(), &notification.Input{
		UserID: 2, Type: "SUCCESS", Category: "PAYMENT", Title: "Paid", Content: "NO1 paid",
		Metadata: map[string]any{"orderNo": "NO1", "wechatSubscribe": subscribe}, Channels: []string{"IN_APP", "SMS", "PUSH"},
	})
	require.NoError(t, err)

	require.Len(t, j.queued, 2)
	assert.Equal(t, entity.NotificationSMSJob{NotificationID: n.ID, UserID: 2, Content: "NO1 paid"}, j.queued[0])

	wechat, ok := j.queued[1].(entity.NotificationWechatSubscribeJob)
	require.True(t, ok)
	assert.JSONEq(t, `{"scene":"orderPaid","values":{"orderNo":"NO1"},"page":"pages/x"}`, string(wechat.Payload))
}

func TestCreateSkipsPushWithoutTruthySubscribePayload(t *testing.T) {
	t.Parallel()

	for _, metadata := range []map[string]any{nil, {"wechatSubscribe": nil}, {"wechatSubscribe": ""}, {"wechatSubscribe": false}} {
		j := &fakeJobs{}

		_, err := newService(&fakeRepo{}, j).Create(t.Context(), &notification.Input{
			UserID: 2, Type: "INFO", Category: "SYSTEM", Title: "T", Content: "C", Metadata: metadata, Channels: []string{"PUSH"},
		})
		require.NoError(t, err)
		assert.Empty(t, j.queued, metadata)
	}
}

func TestCreateFailsWhenQueueingFails(t *testing.T) {
	t.Parallel()

	boom := errors.New("queue down")

	_, err := newService(&fakeRepo{}, &fakeJobs{err: boom}).Create(t.Context(), &notification.Input{
		UserID: 2, Type: "INFO", Category: "SYSTEM", Title: "T", Content: "C", Channels: []string{"EMAIL"},
	})
	assert.ErrorIs(t, err, boom)
}

func TestUpdateSettingsCreatesChangesOrKeepsTheSetting(t *testing.T) {
	t.Parallel()

	flags := repo.NotificationFlags{InApp: true, Email: false, SMS: true, Push: true}

	r := &fakeRepo{}
	_, err := newService(r, &fakeJobs{}).UpdateSettings(t.Context(), 2, "ORDER", flags)
	require.NoError(t, err)
	assert.Equal(t, &flags, r.inserted)

	existing := &entity.NotificationSetting{ID: 4, EnableInApp: true, EnableEmail: false, EnableSMS: true, EnablePush: true}
	r = &fakeRepo{setting: existing}
	got, err := newService(r, &fakeJobs{}).UpdateSettings(t.Context(), 2, "ORDER", flags)
	require.NoError(t, err)
	assert.Same(t, existing, got)
	assert.Nil(t, r.updated)

	changed := flags
	changed.Email = true
	_, err = newService(r, &fakeJobs{}).UpdateSettings(t.Context(), 2, "ORDER", changed)
	require.NoError(t, err)
	assert.Equal(t, &changed, r.updated)
}

func TestMissingNotificationIsNotFound(t *testing.T) {
	t.Parallel()

	s := newService(&fakeRepo{}, &fakeJobs{})

	_, err := s.Get(t.Context(), 1, 2)
	assert.Same(t, notification.ErrNotFound, err)

	_, err = s.MarkAsRead(t.Context(), 1, 2)
	assert.Same(t, notification.ErrNotFound, err)

	assert.Same(t, notification.ErrNotFound, s.Remove(t.Context(), 1, 2))
	assert.Equal(t, "Notification not found", notification.ErrNotFound.Message)
}

func TestRecordChannelResultAddsErrorAndSentAt(t *testing.T) {
	t.Parallel()

	r := &fakeRepo{}
	require.NoError(t, newService(r, &fakeJobs{}).RecordChannelResult(t.Context(), 1, "EMAIL", false, "smtp down"))

	raw, err := json.Marshal(r.recorded)
	require.NoError(t, err)
	assert.Regexp(t, `^\{"EMAIL":\{"error":"smtp down","sent":false,"sentAt":"\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z"\}\}$`, string(raw))
}

func TestCleanerRunsBeforeTheUsersCleaner(t *testing.T) {
	t.Parallel()

	c := notification.NewCleaner(&fakeRepo{})
	assert.Equal(t, "notifications", c.Name())
	assert.Equal(t, 100, c.Order())
}
