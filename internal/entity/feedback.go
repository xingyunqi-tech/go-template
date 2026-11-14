package entity

import (
	"encoding/json"
	"time"
	"uuid"
)

// Feedback types.
const (
	FeedbackBug           = "bug"
	FeedbackSuggestion    = "suggestion"
	FeedbackComplaint     = "complaint"
	FeedbackOther         = "other"
	FeedbackQuestionError = "question_error"
	FeedbackAIContent     = "ai_content"
)

// FeedbackPending is the status of new feedback.
const FeedbackPending = "pending"

// GeneralFeedbackTypes are the types the feedback endpoint accepts and lists by default.
func GeneralFeedbackTypes() []string {
	return []string{FeedbackBug, FeedbackSuggestion, FeedbackComplaint, FeedbackOther}
}

// UserQueryableFeedbackTypes are the types a user can read through the feedback endpoints.
func UserQueryableFeedbackTypes() []string {
	return append(GeneralFeedbackTypes(), FeedbackQuestionError)
}

// Feedback is user feedback or a question report; Images and MessageSnapshot are raw JSON.
type Feedback struct {
	ID              int
	UserID          *int
	Type            string
	Content         string
	Images          json.RawMessage
	Contact         *string
	Status          string
	Reply           *string
	QuestionID      *uuid.UUID
	QuestionVersion *int
	Reason          *string
	ErrataID        *uuid.UUID
	MessageID       *uuid.UUID
	MessageSnapshot json.RawMessage
	CreatedAt       time.Time
	UpdatedAt       time.Time
}
