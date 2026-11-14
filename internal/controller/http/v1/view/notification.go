package view

import (
	"encoding/json"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// Notification is nestjs NotificationMapper.toDomain: metadata and readAt are left out when null.
type Notification struct {
	ID           int             `json:"id"`
	UserID       int             `json:"userId"`
	Type         string          `json:"type"`
	Category     string          `json:"category"`
	Title        string          `json:"title"`
	Content      string          `json:"content"`
	Metadata     json.RawMessage `json:"metadata,omitempty"`
	Channels     []string        `json:"channels"`
	SentChannels json.RawMessage `json:"sentChannels"`
	IsRead       bool            `json:"isRead"`
	ReadAt       *jsontime.Time  `json:"readAt,omitempty"`
	CreatedAt    jsontime.Time   `json:"createdAt"`
	UpdatedAt    jsontime.Time   `json:"updatedAt"`
	DeletedAt    *jsontime.Time  `json:"deletedAt"`
}

// NewNotification returns the view of n.
func NewNotification(n *entity.NotificationRecord) Notification {
	channels := n.Channels
	if channels == nil {
		channels = []string{}
	}

	sent := n.SentChannels
	if len(sent) == 0 {
		sent = json.RawMessage("{}")
	}

	return Notification{
		ID: n.ID, UserID: n.UserID, Type: n.Type, Category: n.Category, Title: n.Title, Content: n.Content,
		Metadata: n.Metadata, Channels: channels, SentChannels: sent, IsRead: n.IsRead, ReadAt: timePtr(n.ReadAt),
		CreatedAt: jsontime.New(n.CreatedAt), UpdatedAt: jsontime.New(n.UpdatedAt), DeletedAt: timePtr(n.DeletedAt),
	}
}

// NotificationPage is the paginated notification list.
type NotificationPage = pagination.Page[Notification]

// NotificationSetting is a notification setting.
type NotificationSetting struct {
	ID          int           `json:"id"`
	UserID      int           `json:"userId"`
	Category    string        `json:"category"`
	EnableInApp bool          `json:"enableInApp"`
	EnableEmail bool          `json:"enableEmail"`
	EnableSMS   bool          `json:"enableSms"`
	EnablePush  bool          `json:"enablePush"`
	CreatedAt   jsontime.Time `json:"createdAt"`
	UpdatedAt   jsontime.Time `json:"updatedAt"`
}

// NewNotificationSetting returns the view of s.
func NewNotificationSetting(s *entity.NotificationSetting) NotificationSetting {
	return NotificationSetting{
		ID: s.ID, UserID: s.UserID, Category: s.Category, EnableInApp: s.EnableInApp, EnableEmail: s.EnableEmail,
		EnableSMS: s.EnableSMS, EnablePush: s.EnablePush, CreatedAt: jsontime.New(s.CreatedAt), UpdatedAt: jsontime.New(s.UpdatedAt),
	}
}
