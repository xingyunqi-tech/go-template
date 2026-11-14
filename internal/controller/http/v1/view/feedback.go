package view

import (
	"encoding/json"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// Feedback is the nestjs Feedback in FeedbackMapper key order; images is absent when null.
type Feedback struct {
	ID              int             `json:"id"`
	UserID          *int            `json:"userId"`
	Type            string          `json:"type"`
	Content         string          `json:"content"`
	Images          json.RawMessage `json:"images,omitempty"`
	Contact         *string         `json:"contact"`
	Status          string          `json:"status"`
	Reply           *string         `json:"reply"`
	QuestionID      *string         `json:"questionId"`
	QuestionVersion *int            `json:"questionVersion"`
	Reason          *string         `json:"reason"`
	ErrataID        *string         `json:"errataId"`
	MessageID       *string         `json:"messageId"`
	MessageSnapshot json.RawMessage `json:"messageSnapshot"`
	CreatedAt       jsontime.Time   `json:"createdAt"`
	UpdatedAt       jsontime.Time   `json:"updatedAt"`
}

func uuidString(id *uuid.UUID) *string {
	if id == nil {
		return nil
	}

	s := id.String()

	return &s
}

// NewFeedback returns the view of f.
func NewFeedback(f *entity.Feedback) Feedback {
	images := f.Images
	if string(images) == "null" {
		images = nil
	}

	return Feedback{
		ID: f.ID, UserID: f.UserID, Type: f.Type, Content: f.Content, Images: images, Contact: f.Contact,
		Status: f.Status, Reply: f.Reply, QuestionID: uuidString(f.QuestionID), QuestionVersion: f.QuestionVersion,
		Reason: f.Reason, ErrataID: uuidString(f.ErrataID), MessageID: uuidString(f.MessageID),
		MessageSnapshot: f.MessageSnapshot, CreatedAt: jsontime.New(f.CreatedAt), UpdatedAt: jsontime.New(f.UpdatedAt),
	}
}

// FeedbackPage is the feedback list response.
type FeedbackPage = pagination.Page[Feedback]
