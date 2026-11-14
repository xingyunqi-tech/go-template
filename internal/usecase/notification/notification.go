// Package notification is nestjs NotificationsService: in-app notifications, per-category
// channel settings and the queueing of the external channels.
package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

const _cleanerOrder = 100

var (
	// ErrNotFound is the 404 of a notification the user cannot see.
	ErrNotFound = &entity.Error{
		Status: entity.StatusNotFound, Message: "Notification not found", ErrorKey: "notificationNotFound", Fields: nil,
	}
	// ErrSettingNotFound is the 404 of a setting removed while it was updated.
	ErrSettingNotFound = &entity.Error{
		Status: entity.StatusNotFound, Message: "Notification setting not found", ErrorKey: "settingNotFound", Fields: nil,
	}
)

// Input is nestjs CreateNotificationDto.
type Input struct {
	UserID   int
	Type     string
	Category string
	Title    string
	Content  string
	Metadata map[string]any
	Channels []string
}

// Service is nestjs NotificationsService.
type Service struct {
	repo repo.Notifications
	jobs repo.Jobs
	log  *slog.Logger
	now  func() time.Time
}

// New returns a Service.
func New(notifications repo.Notifications, jobs repo.Jobs, log *slog.Logger) *Service {
	return &Service{repo: notifications, jobs: jobs, log: log, now: time.Now}
}

// readAt is JavaScript new Date(): UTC with millisecond precision.
func (s *Service) readAt() time.Time {
	return s.now().UTC().Truncate(time.Millisecond)
}

// Create stores a notification on the channels the user's setting for its category allows and
// queues the external channels.
func (s *Service) Create(ctx context.Context, in *Input) (*entity.NotificationRecord, error) {
	s.log.InfoContext(ctx, "creating notification", "user_id", in.UserID)

	setting, err := s.repo.Setting(ctx, in.UserID, in.Category)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	channels := make([]string, 0, len(in.Channels))

	for _, c := range in.Channels {
		if setting == nil || setting.Allows(c) {
			channels = append(channels, c)
		}
	}

	var metadata []byte

	if in.Metadata != nil {
		if metadata, err = json.Marshal(in.Metadata); err != nil {
			return nil, fmt.Errorf("notification: metadata: %w", err)
		}
	}

	n, err := s.repo.Create(ctx, &repo.NewNotification{
		UserID: in.UserID, Type: in.Type, Category: in.Category, Title: in.Title, Content: in.Content,
		Metadata: metadata, Channels: channels,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if err := s.queueExternal(ctx, n); err != nil {
		return nil, err
	}

	s.log.InfoContext(ctx, "notification created", "notification_id", n.ID)

	return n, nil
}

func (s *Service) queueExternal(ctx context.Context, n *entity.NotificationRecord) error {
	subscribe := wechatSubscribe(n.Metadata)

	for _, c := range n.Channels {
		var job entity.NotificationJob

		switch {
		case c == entity.ChannelEmail:
			job = entity.NotificationEmailJob{NotificationID: n.ID, UserID: n.UserID, Title: n.Title, Content: n.Content}
		case c == entity.ChannelSMS:
			job = entity.NotificationSMSJob{NotificationID: n.ID, UserID: n.UserID, Content: n.Content}
		case c == entity.ChannelPush && subscribe != nil:
			job = entity.NotificationWechatSubscribeJob{NotificationID: n.ID, UserID: n.UserID, Payload: subscribe}
		default:
			continue
		}

		if err := s.jobs.EnqueueNotification(ctx, job); err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}
	}

	return nil
}

// wechatSubscribe returns metadata.wechatSubscribe when it is truthy.
func wechatSubscribe(metadata json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(metadata, &fields) != nil {
		return nil
	}

	v := fields["wechatSubscribe"]

	switch string(v) {
	case "", "null", "false", "0", `""`:
		return nil
	default:
		return v
	}
}

// CreateInAppBatch writes one in-app notification to every user that did not turn in-app off for
// the category and returns the number written.
func (s *Service) CreateInAppBatch(ctx context.Context, b *repo.InAppBatch) (int, error) {
	return s.repo.CreateInApp(ctx, b) //nolint:wrapcheck // repo errors are wrapped
}

// RecordChannelResult merges the delivery result of channel into sentChannels.
func (s *Service) RecordChannelResult(ctx context.Context, id int, channel string, sent bool, failure string) error {
	result := map[string]any{"sent": sent}
	if failure != "" {
		result["error"] = failure
	}

	result["sentAt"] = jsontime.New(s.now()).String()

	return s.repo.RecordChannel(ctx, id, channel, result) //nolint:wrapcheck // repo errors are wrapped
}

// List returns a page of the user's notifications, newest first, and the total.
func (s *Service) List(ctx context.Context, userID int, f repo.NotificationFilter, page, limit int) ([]entity.NotificationRecord, int, error) {
	list, err := s.repo.List(ctx, userID, f, (page-1)*limit, limit)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	total, err := s.repo.Count(ctx, userID, f)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	return list, total, nil
}

// UnreadCount counts the user's unread notifications.
func (s *Service) UnreadCount(ctx context.Context, userID int) (int, error) {
	unread := false

	return s.repo.Count(ctx, userID, repo.NotificationFilter{Category: nil, IsRead: &unread}) //nolint:wrapcheck // repo errors are wrapped
}

// Get returns one notification of the user.
func (s *Service) Get(ctx context.Context, id, userID int) (*entity.NotificationRecord, error) {
	n, err := s.repo.Get(ctx, id, userID)

	switch {
	case err != nil:
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	case n == nil:
		return nil, ErrNotFound
	default:
		return n, nil
	}
}

// MarkAsRead marks one notification of the user as read now.
func (s *Service) MarkAsRead(ctx context.Context, id, userID int) (*entity.NotificationRecord, error) {
	n, err := s.repo.MarkRead(ctx, id, userID, s.readAt())

	switch {
	case err != nil:
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	case n == nil:
		return nil, ErrNotFound
	}

	s.log.InfoContext(ctx, "notification marked as read", "notification_id", id, "user_id", userID)

	return n, nil
}

// MarkAllAsRead marks every unread notification of the user as read now.
func (s *Service) MarkAllAsRead(ctx context.Context, userID int) error {
	if err := s.repo.MarkAllRead(ctx, userID, s.readAt()); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	s.log.InfoContext(ctx, "all notifications marked as read", "user_id", userID)

	return nil
}

// Remove soft-deletes one notification of the user.
func (s *Service) Remove(ctx context.Context, id, userID int) error {
	deleted, err := s.repo.SoftDelete(ctx, id, userID)

	switch {
	case err != nil:
		return err //nolint:wrapcheck // repo errors are wrapped
	case !deleted:
		return ErrNotFound
	}

	s.log.InfoContext(ctx, "notification deleted", "notification_id", id, "user_id", userID)

	return nil
}

// Settings lists the user's settings.
func (s *Service) Settings(ctx context.Context, userID int) ([]entity.NotificationSetting, error) {
	return s.repo.Settings(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}

// UpdateSettings creates or changes the user's setting of category; an unchanged setting keeps
// its updatedAt.
func (s *Service) UpdateSettings(ctx context.Context, userID int, category string, f repo.NotificationFlags) (*entity.NotificationSetting, error) {
	setting, err := s.repo.Setting(ctx, userID, category)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	switch {
	case setting == nil:
		setting, err = s.repo.CreateSetting(ctx, userID, category, f)
	case f != (repo.NotificationFlags{InApp: setting.EnableInApp, Email: setting.EnableEmail, SMS: setting.EnableSMS, Push: setting.EnablePush}):
		setting, err = s.repo.UpdateSetting(ctx, setting.ID, f)
	}

	switch {
	case err != nil:
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	case setting == nil:
		return nil, ErrSettingNotFound
	}

	s.log.InfoContext(ctx, "notification settings updated", "user_id", userID, "category", category)

	return setting, nil
}

// InitializeDefaultSettings creates the missing settings of every category with all channels on.
func (s *Service) InitializeDefaultSettings(ctx context.Context, userID int) error {
	if err := s.repo.InitSettings(ctx, userID, entity.NotificationCategories()); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	s.log.InfoContext(ctx, "default notification settings initialized", "user_id", userID)

	return nil
}

// Cleaner removes the notifications and settings of a deleted account.
type Cleaner struct {
	repo repo.Notifications
}

// NewCleaner returns the notifications cleaner (order 100).
func NewCleaner(notifications repo.Notifications) *Cleaner {
	return &Cleaner{repo: notifications}
}

// Name implements accountcleanup.Cleaner.
func (*Cleaner) Name() string { return "notifications" }

// Order implements accountcleanup.Cleaner.
func (*Cleaner) Order() int { return _cleanerOrder }

// Clean implements accountcleanup.Cleaner.
func (c *Cleaner) Clean(ctx context.Context, userID int) error {
	return c.repo.RemoveUser(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}
