// Package events publishes the events other services consume (platform conventions §10) as
// CloudEvents 1.0 JSON.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
	"uuid"
)

// TypeUserDeleted is published once a deleted user is purged.
const TypeUserDeleted = "user.deleted"

// Event is a CloudEvents 1.0 event in JSON form.
type Event struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Time            time.Time       `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

// UserDeleted is the data of user.deleted.
type UserDeleted struct {
	UserID uuid.UUID `json:"userId"`
}

// NewUserDeleted returns the user.deleted event of userID published by source.
func NewUserDeleted(source string, userID uuid.UUID, now time.Time) (*Event, error) {
	data, err := json.Marshal(UserDeleted{UserID: userID})
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}

	return &Event{
		SpecVersion: "1.0", ID: uuid.NewV7().String(), Source: source, Type: TypeUserDeleted,
		Time: now.UTC(), DataContentType: "application/json", Data: data,
	}, nil
}

// Publisher sends events to the transport the monorepo configures.
type Publisher interface {
	Publish(ctx context.Context, e *Event) error
}

// Log only logs the events.
type Log struct {
	log *slog.Logger
}

var _ Publisher = (*Log)(nil)

// NewLog returns a Log.
func NewLog(log *slog.Logger) *Log {
	return &Log{log: log}
}

// Publish implements Publisher.
func (l *Log) Publish(ctx context.Context, e *Event) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("events: %w", err)
	}

	l.log.InfoContext(ctx, "event published", "type", e.Type, "event", string(raw))

	return nil
}
