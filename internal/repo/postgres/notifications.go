package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Notifications implements repo.Notifications.
type Notifications struct {
	db *DB
}

// NewNotifications returns Notifications.
func NewNotifications(db *DB) *Notifications {
	return &Notifications{db: db}
}

var _ repo.Notifications = (*Notifications)(nil)

func toNotification(n *sqlcgen.Notification) (*entity.NotificationRecord, error) {
	out := &entity.NotificationRecord{
		ID: int(n.ID), UserID: int(n.UserId), Type: n.Type, Category: n.Category, Title: n.Title,
		Content: n.Content, Metadata: nil, Channels: []string{}, SentChannels: n.SentChannels,
		IsRead: n.IsRead, ReadAt: utcPtr(n.ReadAt), CreatedAt: n.CreatedAt.UTC(), UpdatedAt: n.UpdatedAt.UTC(),
		DeletedAt: utcPtr(n.DeletedAt),
	}

	if n.Metadata != nil && string(n.Metadata) != "null" {
		out.Metadata = n.Metadata
	}

	if err := json.Unmarshal(n.Channels, &out.Channels); err != nil {
		return nil, fmt.Errorf("postgres: notification %d channels: %w", n.ID, err)
	}

	return out, nil
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}

	u := t.UTC()

	return &u
}

func toSetting(s *sqlcgen.NotificationSetting) *entity.NotificationSetting {
	return &entity.NotificationSetting{
		ID: int(s.ID), UserID: int(s.UserId), Category: s.Category, EnableInApp: s.EnableInApp,
		EnableEmail: s.EnableEmail, EnableSMS: s.EnableSms, EnablePush: s.EnablePush,
		CreatedAt: s.CreatedAt.UTC(), UpdatedAt: s.UpdatedAt.UTC(),
	}
}

// List implements repo.Notifications.
func (r *Notifications) List(ctx context.Context, userID int, f repo.NotificationFilter, offset, limit int) ([]entity.NotificationRecord, error) {
	var rows []*sqlcgen.Notification

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		rows, err = q.ListNotifications(ctx, sqlcgen.ListNotificationsParams{
			UserID: id32(userID), Category: f.Category, IsRead: f.IsRead, RowOffset: id32(offset), RowLimit: id32(limit),
		})

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list notifications: %w", err)
	}

	out := make([]entity.NotificationRecord, 0, len(rows))

	for _, row := range rows {
		n, err := toNotification(row)
		if err != nil {
			return nil, err
		}

		out = append(out, *n)
	}

	return out, nil
}

// Count implements repo.Notifications.
func (r *Notifications) Count(ctx context.Context, userID int, f repo.NotificationFilter) (int, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.CountNotifications(ctx, sqlcgen.CountNotificationsParams{UserID: id32(userID), Category: f.Category, IsRead: f.IsRead})

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("postgres: count notifications: %w", err)
	}

	return int(n), nil
}

func (r *Notifications) one(ctx context.Context, name string, fn func(*sqlcgen.Queries) (*sqlcgen.Notification, error)) (*entity.NotificationRecord, error) {
	var row *sqlcgen.Notification

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		row, err = fn(q)
		if notFound(err) {
			row = nil

			return nil
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: %s: %w", name, err)
	}

	if row == nil {
		return nil, nil //nolint:nilnil // not found
	}

	return toNotification(row)
}

// Get implements repo.Notifications.
func (r *Notifications) Get(ctx context.Context, id, userID int) (*entity.NotificationRecord, error) {
	return r.one(ctx, "get notification", func(q *sqlcgen.Queries) (*sqlcgen.Notification, error) {
		return q.GetNotification(ctx, sqlcgen.GetNotificationParams{ID: id32(id), UserID: id32(userID)}) //nolint:wrapcheck // wrapped by one
	})
}

// MarkRead implements repo.Notifications.
func (r *Notifications) MarkRead(ctx context.Context, id, userID int, at time.Time) (*entity.NotificationRecord, error) {
	return r.one(ctx, "mark notification read", func(q *sqlcgen.Queries) (*sqlcgen.Notification, error) {
		return q.MarkNotificationRead(ctx, sqlcgen.MarkNotificationReadParams{ //nolint:wrapcheck // wrapped by one
			ReadAt: &at, ID: id32(id), UserID: id32(userID),
		})
	})
}

// MarkAllRead implements repo.Notifications.
func (r *Notifications) MarkAllRead(ctx context.Context, userID int, at time.Time) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		return q.MarkAllNotificationsRead(ctx, sqlcgen.MarkAllNotificationsReadParams{ReadAt: &at, UserID: id32(userID)})
	})
	if err != nil {
		return fmt.Errorf("postgres: mark all notifications read: %w", err)
	}

	return nil
}

// SoftDelete implements repo.Notifications.
func (r *Notifications) SoftDelete(ctx context.Context, id, userID int) (bool, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.SoftDeleteNotification(ctx, sqlcgen.SoftDeleteNotificationParams{ID: id32(id), UserID: id32(userID)})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("postgres: delete notification: %w", err)
	}

	return n > 0, nil
}

// Create implements repo.Notifications.
func (r *Notifications) Create(ctx context.Context, n *repo.NewNotification) (*entity.NotificationRecord, error) {
	channels, err := json.Marshal(n.Channels)
	if err != nil {
		return nil, fmt.Errorf("postgres: notification channels: %w", err)
	}

	return r.one(ctx, "create notification", func(q *sqlcgen.Queries) (*sqlcgen.Notification, error) {
		return q.CreateNotification(ctx, sqlcgen.CreateNotificationParams{ //nolint:wrapcheck // wrapped by one
			UserID: id32(n.UserID), Type: n.Type, Category: n.Category, Title: n.Title, Content: n.Content,
			Metadata: n.Metadata, Channels: channels,
		})
	})
}

// CreateInApp implements repo.Notifications.
func (r *Notifications) CreateInApp(ctx context.Context, b *repo.InAppBatch) (int, error) {
	if len(b.UserIDs) == 0 {
		return 0, nil
	}

	ids := make([]int32, len(b.UserIDs))
	for i, id := range b.UserIDs {
		ids[i] = id32(id)
	}

	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.InsertInAppNotifications(ctx, sqlcgen.InsertInAppNotificationsParams{
			Type: b.Type, Category: b.Category, Title: b.Title, Content: b.Content, Metadata: b.Metadata, UserIds: ids,
		})

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("postgres: insert in-app notifications: %w", err)
	}

	return int(n), nil
}

// RecordChannel implements repo.Notifications.
func (r *Notifications) RecordChannel(ctx context.Context, id int, channel string, result map[string]any) error {
	patch, err := json.Marshal(map[string]any{channel: result})
	if err != nil {
		return fmt.Errorf("postgres: channel result: %w", err)
	}

	err = r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		return q.RecordNotificationChannel(ctx, sqlcgen.RecordNotificationChannelParams{Patch: patch, ID: id32(id)})
	})
	if err != nil {
		return fmt.Errorf("postgres: record channel result: %w", err)
	}

	return nil
}

func (r *Notifications) setting(ctx context.Context, name string, fn func(*sqlcgen.Queries) (*sqlcgen.NotificationSetting, error)) (*entity.NotificationSetting, error) {
	var row *sqlcgen.NotificationSetting

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		row, err = fn(q)
		if notFound(err) {
			row = nil

			return nil
		}

		return err
	})

	switch {
	case err != nil:
		return nil, fmt.Errorf("postgres: %s: %w", name, err)
	case row == nil:
		return nil, nil //nolint:nilnil // not found
	default:
		return toSetting(row), nil
	}
}

// Setting implements repo.Notifications.
func (r *Notifications) Setting(ctx context.Context, userID int, category string) (*entity.NotificationSetting, error) {
	return r.setting(ctx, "get notification setting", func(q *sqlcgen.Queries) (*sqlcgen.NotificationSetting, error) {
		return q.GetNotificationSetting(ctx, sqlcgen.GetNotificationSettingParams{ //nolint:wrapcheck // wrapped by setting
			UserID: id32(userID), Category: category,
		})
	})
}

// Settings implements repo.Notifications.
func (r *Notifications) Settings(ctx context.Context, userID int) ([]entity.NotificationSetting, error) {
	var rows []*sqlcgen.NotificationSetting

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		rows, err = q.ListNotificationSettings(ctx, id32(userID))

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list notification settings: %w", err)
	}

	out := make([]entity.NotificationSetting, len(rows))
	for i, row := range rows {
		out[i] = *toSetting(row)
	}

	return out, nil
}

// CreateSetting implements repo.Notifications.
func (r *Notifications) CreateSetting(ctx context.Context, userID int, category string, f repo.NotificationFlags) (*entity.NotificationSetting, error) {
	return r.setting(ctx, "create notification setting", func(q *sqlcgen.Queries) (*sqlcgen.NotificationSetting, error) {
		return q.CreateNotificationSetting(ctx, sqlcgen.CreateNotificationSettingParams{ //nolint:wrapcheck // wrapped by setting
			UserID: id32(userID), Category: category, EnableInApp: f.InApp, EnableEmail: f.Email, EnableSms: f.SMS, EnablePush: f.Push,
		})
	})
}

// UpdateSetting implements repo.Notifications.
func (r *Notifications) UpdateSetting(ctx context.Context, id int, f repo.NotificationFlags) (*entity.NotificationSetting, error) {
	return r.setting(ctx, "update notification setting", func(q *sqlcgen.Queries) (*sqlcgen.NotificationSetting, error) {
		return q.UpdateNotificationSetting(ctx, sqlcgen.UpdateNotificationSettingParams{ //nolint:wrapcheck // wrapped by setting
			EnableInApp: f.InApp, EnableEmail: f.Email, EnableSms: f.SMS, EnablePush: f.Push, ID: id32(id),
		})
	})
}

// InitSettings implements repo.Notifications; inside a request transaction it runs under a
// savepoint, so a failure leaves the transaction usable.
func (r *Notifications) InitSettings(ctx context.Context, userID int, categories []string) error {
	params := sqlcgen.InitNotificationSettingsParams{UserID: id32(userID), Categories: categories}

	s := stateOf(ctx)
	if s == nil {
		err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.InitNotificationSettings(ctx, params) })
		if err != nil {
			return fmt.Errorf("postgres: init notification settings: %w", err)
		}

		return nil
	}

	savepoint, err := s.tx.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: init notification settings: savepoint: %w", err)
	}

	if err := sqlcgen.New(savepoint).InitNotificationSettings(ctx, params); err != nil {
		_ = savepoint.Rollback(ctx)

		return fmt.Errorf("postgres: init notification settings: %w", err)
	}

	if err := savepoint.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: init notification settings: release savepoint: %w", err)
	}

	return nil
}

// RemoveUser implements repo.Notifications.
func (r *Notifications) RemoveUser(ctx context.Context, userID int) error {
	err := r.db.InTx(ctx, func(ctx context.Context) error {
		return r.db.queries(ctx, func(q *sqlcgen.Queries) error {
			if err := q.DeleteUserNotifications(ctx, id32(userID)); err != nil {
				return err //nolint:wrapcheck // wrapped below
			}

			return q.DeleteUserNotificationSettings(ctx, id32(userID)) //nolint:wrapcheck // wrapped below
		})
	})
	if err != nil {
		return fmt.Errorf("postgres: remove user notifications: %w", err)
	}

	return nil
}
