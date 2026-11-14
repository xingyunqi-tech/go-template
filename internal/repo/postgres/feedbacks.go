package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Feedbacks implements repo.Feedbacks.
type Feedbacks struct {
	db *DB
}

// NewFeedbacks returns the feedback repository.
func NewFeedbacks(db *DB) *Feedbacks {
	return &Feedbacks{db: db}
}

var _ repo.Feedbacks = (*Feedbacks)(nil)

func toFeedback(f *sqlcgen.Feedback) *entity.Feedback {
	return &entity.Feedback{
		ID: int(f.ID), UserID: intPtr(f.UserId), Type: f.Type, Content: f.Content, Images: f.Images,
		Contact: f.Contact, Status: f.Status, Reply: f.Reply, QuestionID: f.QuestionId,
		QuestionVersion: intPtr(f.QuestionVersion), Reason: f.Reason, ErrataID: f.ErrataId, MessageID: f.MessageId,
		MessageSnapshot: f.MessageSnapshot, CreatedAt: f.CreatedAt, UpdatedAt: f.UpdatedAt,
	}
}

// Create implements repo.Feedbacks.
func (r *Feedbacks) Create(ctx context.Context, f *repo.NewFeedback) (*entity.Feedback, error) {
	var out *entity.Feedback

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.CreateFeedback(ctx, sqlcgen.CreateFeedbackParams{
			UserID: int32Ptr(&f.UserID), Type: f.Type, Content: f.Content, Images: f.Images, Contact: f.Contact,
		})
		if err == nil {
			out = toFeedback(row)
		}

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("feedbacks: create: %w", err)
	}

	return out, nil
}

// Get implements repo.Feedbacks.
func (r *Feedbacks) Get(ctx context.Context, id int) (*entity.Feedback, error) {
	out, err := findRow(ctx, r.db, func(q *sqlcgen.Queries) (*sqlcgen.Feedback, error) {
		return q.GetFeedback(ctx, id32(id)) //nolint:wrapcheck // wrapped below
	}, toFeedback)
	if err != nil {
		return nil, fmt.Errorf("feedbacks: get: %w", err)
	}

	return out, nil
}

// List implements repo.Feedbacks.
func (r *Feedbacks) List(ctx context.Context, f *repo.FeedbackFilter, offset, limit int64) ([]entity.Feedback, error) {
	var out []entity.Feedback

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListUserFeedbacks(ctx, sqlcgen.ListUserFeedbacksParams{
			UserID: int32Ptr(&f.UserID), Types: f.Types, Status: nonEmpty(f.Status), RowOffset: offset, RowLimit: limit,
		})
		for _, row := range rows {
			out = append(out, *toFeedback(row))
		}

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("feedbacks: list: %w", err)
	}

	return out, nil
}

// Count implements repo.Feedbacks.
func (r *Feedbacks) Count(ctx context.Context, f *repo.FeedbackFilter) (int, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.CountUserFeedbacks(ctx, sqlcgen.CountUserFeedbacksParams{
			UserID: int32Ptr(&f.UserID), Types: f.Types, Status: nonEmpty(f.Status),
		})

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return 0, fmt.Errorf("feedbacks: count: %w", err)
	}

	return int(n), nil
}

// AnonymizeByUser implements repo.Feedbacks.
func (r *Feedbacks) AnonymizeByUser(ctx context.Context, userID int) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.AnonymizeUserFeedbacks(ctx, int32Ptr(&userID)) })
	if err != nil {
		return fmt.Errorf("feedbacks: anonymize: %w", err)
	}

	return nil
}
