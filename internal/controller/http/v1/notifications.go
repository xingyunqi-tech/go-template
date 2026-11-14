package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_defaultNotificationsLimit = 10
	_maxNotificationsLimit     = 50
)

func notificationFilter(q *validatorx.Object) repo.NotificationFilter {
	f := repo.NotificationFilter{Category: nil, IsRead: nil}

	if v, ok := field(q, "category"); ok {
		if s, isString := v.(string); isString && s != "" {
			f.Category = &s
		}
	}

	if v, ok := field(q, "isRead"); ok {
		if b, isBool := v.(bool); isBool {
			f.IsRead = &b
		}
	}

	return f
}

// GetNotifications handles GET notifications.
func (s *Server) GetNotifications(ctx context.Context, _ gen.GetNotificationsRequestObject) (gen.GetNotificationsResponseObject, error) {
	q := query(ctx)
	page := number(q, "page", 1)
	limit := min(number(q, "limit", _defaultNotificationsLimit), _maxNotificationsLimit)

	list, total, err := s.notifications.List(ctx, reqctx.Principal(ctx).UserID, notificationFilter(q), page, limit)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	items := make([]view.Notification, len(list))
	for i := range list {
		items[i] = view.NewNotification(&list[i])
	}

	return gen.GetNotifications200JSONResponse(pagination.New(items, float64(total), float64(page), float64(limit))), nil
}

// GetUnreadCount handles GET notifications/unread-count.
func (s *Server) GetUnreadCount(ctx context.Context, _ gen.GetUnreadCountRequestObject) (gen.GetUnreadCountResponseObject, error) {
	n, err := s.notifications.UnreadCount(ctx, reqctx.Principal(ctx).UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetUnreadCount200JSONResponse(n), nil
}

// GetNotificationSettings handles GET notifications/settings.
func (s *Server) GetNotificationSettings(ctx context.Context, _ gen.GetNotificationSettingsRequestObject) (gen.GetNotificationSettingsResponseObject, error) {
	settings, err := s.notifications.Settings(ctx, reqctx.Principal(ctx).UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	out := make(gen.GetNotificationSettings200JSONResponse, len(settings))
	for i := range settings {
		out[i] = view.NewNotificationSetting(&settings[i])
	}

	return out, nil
}

// UpdateNotificationSettings handles PUT notifications/settings.
func (s *Server) UpdateNotificationSettings(
	ctx context.Context, _ gen.UpdateNotificationSettingsRequestObject,
) (gen.UpdateNotificationSettingsResponseObject, error) {
	b := body(ctx)
	flag := func(key string) bool {
		v, _ := field(b, key)
		on, _ := v.(bool)

		return on
	}

	category, _ := field(b, "category")

	setting, err := s.notifications.UpdateSettings(ctx, reqctx.Principal(ctx).UserID, validatorx.ToString(category), repo.NotificationFlags{
		InApp: flag("enableInApp"), Email: flag("enableEmail"), SMS: flag("enableSms"), Push: flag("enablePush"),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.UpdateNotificationSettings200JSONResponse(view.NewNotificationSetting(setting)), nil
}

// GetNotification handles GET notifications/:id.
func (s *Server) GetNotification(ctx context.Context, req gen.GetNotificationRequestObject) (gen.GetNotificationResponseObject, error) {
	n, err := s.notifications.Get(ctx, int(req.Id), reqctx.Principal(ctx).UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetNotification200JSONResponse(view.NewNotification(n)), nil
}

// MarkAllNotificationsAsRead handles POST notifications/read-all.
func (s *Server) MarkAllNotificationsAsRead(
	ctx context.Context, _ gen.MarkAllNotificationsAsReadRequestObject,
) (gen.MarkAllNotificationsAsReadResponseObject, error) {
	if err := s.notifications.MarkAllAsRead(ctx, reqctx.Principal(ctx).UserID); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.MarkAllNotificationsAsRead200Response{Body: nil}, nil
}

// MarkNotificationAsRead handles POST notifications/:id/read.
func (s *Server) MarkNotificationAsRead(ctx context.Context, req gen.MarkNotificationAsReadRequestObject) (gen.MarkNotificationAsReadResponseObject, error) {
	n, err := s.notifications.MarkAsRead(ctx, int(req.Id), reqctx.Principal(ctx).UserID)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.MarkNotificationAsRead200JSONResponse(view.NewNotification(n)), nil
}

// DeleteNotification handles DELETE notifications/:id.
func (s *Server) DeleteNotification(ctx context.Context, req gen.DeleteNotificationRequestObject) (gen.DeleteNotificationResponseObject, error) {
	if err := s.notifications.Remove(ctx, int(req.Id), reqctx.Principal(ctx).UserID); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.DeleteNotification200Response{Body: nil}, nil
}
