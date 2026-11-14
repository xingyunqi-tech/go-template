package entity

import (
	"encoding/json"
	"time"
)

// Notification channels, as nestjs NotificationChannel.
const (
	ChannelInApp = "IN_APP"
	ChannelEmail = "EMAIL"
	ChannelSMS   = "SMS"
	ChannelPush  = "PUSH"
)

// NotificationCategories lists nestjs NotificationCategory in declaration order.
func NotificationCategories() []string {
	return []string{"ORDER", "PAYMENT", "SYSTEM", "PROMOTION", "ACTIVITY", "STUDY"}
}

// NotificationRecord is a stored notification; Metadata and SentChannels keep the jsonb text and
// Metadata is nil for SQL null.
type NotificationRecord struct {
	ID           int
	UserID       int
	Type         string
	Category     string
	Title        string
	Content      string
	Metadata     json.RawMessage
	Channels     []string
	SentChannels json.RawMessage
	IsRead       bool
	ReadAt       *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    *time.Time
}

// NotificationSetting is the per-category channel switch of a user.
type NotificationSetting struct {
	ID          int
	UserID      int
	Category    string
	EnableInApp bool
	EnableEmail bool
	EnableSMS   bool
	EnablePush  bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Allows reports whether the setting lets channel through; unknown channels are dropped.
func (s *NotificationSetting) Allows(channel string) bool {
	switch channel {
	case ChannelInApp:
		return s.EnableInApp
	case ChannelEmail:
		return s.EnableEmail
	case ChannelSMS:
		return s.EnableSMS
	case ChannelPush:
		return s.EnablePush
	default:
		return false
	}
}

// NotificationJob is a job of the notification queue.
type NotificationJob interface {
	Kind() string
}

// NotificationEmailJob sends a notification by mail.
type NotificationEmailJob struct {
	NotificationID int    `json:"notificationId"`
	UserID         int    `json:"userId"`
	Title          string `json:"title"`
	Content        string `json:"content"`
}

// Kind is the nestjs job name.
func (NotificationEmailJob) Kind() string { return "send-email" }

// NotificationSMSJob sends a notification by SMS.
type NotificationSMSJob struct {
	NotificationID int    `json:"notificationId"`
	UserID         int    `json:"userId"`
	Content        string `json:"content"`
}

// Kind is the nestjs job name.
func (NotificationSMSJob) Kind() string { return "send-sms" }

// NotificationWechatSubscribeJob sends metadata.wechatSubscribe as a WeChat subscribe message.
type NotificationWechatSubscribeJob struct {
	NotificationID int             `json:"notificationId"`
	UserID         int             `json:"userId"`
	Payload        json.RawMessage `json:"payload"`
}

// Kind is the nestjs job name.
func (NotificationWechatSubscribeJob) Kind() string { return "send-wechat-subscribe" }
