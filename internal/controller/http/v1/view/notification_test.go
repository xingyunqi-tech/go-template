package view_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

func encode(t *testing.T, v any) string {
	t.Helper()

	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	require.NoError(t, enc.Encode(v))

	return string(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
}

func TestNotificationOmitsNullMetadataAndReadAt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 678_000_000, time.UTC)
	n := &entity.NotificationRecord{
		ID: 1, UserID: 2, Type: "INFO", Category: "SYSTEM", Title: "T", Content: "<b>C</b>",
		Channels: []string{"IN_APP"}, SentChannels: json.RawMessage(`{"EMAIL": {"sent": true}}`), CreatedAt: at, UpdatedAt: at,
	}

	assert.Equal(t, `{"id":1,"userId":2,"type":"INFO","category":"SYSTEM","title":"T","content":"<b>C</b>","channels":["IN_APP"],`+
		`"sentChannels":{"EMAIL":{"sent":true}},"isRead":false,"createdAt":"2026-01-02T03:04:05.678Z",`+
		`"updatedAt":"2026-01-02T03:04:05.678Z","deletedAt":null}`, encode(t, view.NewNotification(n)))
}

func TestNotificationKeepsMetadataOrderAndReadAtPosition(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	n := &entity.NotificationRecord{
		ID: 1, UserID: 2, Type: "INFO", Category: "ORDER", Title: "T", Content: "C",
		Metadata: json.RawMessage(`{"orderNo": "N1", "orderId": 5}`), IsRead: true, ReadAt: &at, DeletedAt: &at,
		CreatedAt: at, UpdatedAt: at,
	}

	assert.Equal(t, `{"id":1,"userId":2,"type":"INFO","category":"ORDER","title":"T","content":"C","metadata":{"orderNo":"N1","orderId":5},`+
		`"channels":[],"sentChannels":{},"isRead":true,"readAt":"2026-01-02T03:04:05.000Z","createdAt":"2026-01-02T03:04:05.000Z",`+
		`"updatedAt":"2026-01-02T03:04:05.000Z","deletedAt":"2026-01-02T03:04:05.000Z"}`, encode(t, view.NewNotification(n)))
}

func TestNotificationSettingFieldOrder(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s := &entity.NotificationSetting{ID: 3, UserID: 2, Category: "ORDER", EnableInApp: true, EnableSMS: true, CreatedAt: at, UpdatedAt: at}

	assert.Equal(t, `{"id":3,"userId":2,"category":"ORDER","enableInApp":true,"enableEmail":false,"enableSms":true,"enablePush":false,`+
		`"createdAt":"2026-01-02T03:04:05.000Z","updatedAt":"2026-01-02T03:04:05.000Z"}`, encode(t, view.NewNotificationSetting(s)))
}

func TestNotificationPageIsTheStandardPagination(t *testing.T) {
	t.Parallel()

	page := pagination.New[view.Notification](nil, 0, 1, 10)

	assert.Equal(t, `{"list":[],"total":0,"page":1,"limit":10,"totalPages":0}`, encode(t, page))
}
