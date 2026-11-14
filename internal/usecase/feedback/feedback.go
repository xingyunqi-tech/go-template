// Package feedback stores user feedback like the nestjs FeedbacksService: submission, a list
// limited to the general types by default and details of the user's own queryable feedback.
package feedback

import (
	"context"
	"slices"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

const _cleanerOrder = 90

// Input is a submission; Images is the JSON array, or nil when absent.
type Input struct {
	Type    string
	Content string
	Images  []byte
	Contact *string
}

// List selects a page of the user's feedback; an empty Type lists the general types.
type List struct {
	UserID int
	Type   string
	Status string
	Page   float64
	Limit  float64
}

// Service manages feedback.
type Service struct {
	feedbacks repo.Feedbacks
}

// New returns a Service.
func New(feedbacks repo.Feedbacks) *Service {
	return &Service{feedbacks: feedbacks}
}

// Create stores a pending feedback of the user.
func (s *Service) Create(ctx context.Context, userID int, in *Input) (*entity.Feedback, error) {
	f, err := s.feedbacks.Create(ctx, &repo.NewFeedback{
		UserID: userID, Type: in.Type, Content: in.Content, Images: in.Images, Contact: in.Contact,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return f, nil
}

// List returns a page of the user's feedback, newest first, and the total, which is skip plus
// the rows found unless the page is full or empty past the first row (TypeORM findAndCount).
func (s *Service) List(ctx context.Context, q *List) ([]entity.Feedback, float64, error) {
	filter := &repo.FeedbackFilter{UserID: q.UserID, Types: entity.GeneralFeedbackTypes(), Status: q.Status}
	if q.Type != "" {
		filter.Types = []string{q.Type}
	}

	offset, limit := pagination.OffsetLimit(q.Page, q.Limit)

	data, err := s.feedbacks.List(ctx, filter, offset, limit)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	if total, ok := pagination.LazyTotal(len(data), q.Page, q.Limit); ok {
		return data, total, nil
	}

	total, err := s.feedbacks.Count(ctx, filter)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	return data, float64(total), nil
}

// Get returns the user's feedback of a queryable type, or a 404 feedbackNotFound.
func (s *Service) Get(ctx context.Context, id, userID int) (*entity.Feedback, error) {
	f, err := s.feedbacks.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if f == nil || f.UserID == nil || *f.UserID != userID || !slices.Contains(entity.UserQueryableFeedbackTypes(), f.Type) {
		return nil, entity.NotFoundKey("feedbackNotFound")
	}

	return f, nil
}

// Cleaner detaches the feedback of a removed account and clears its contact and images.
type Cleaner struct {
	feedbacks repo.Feedbacks
}

// NewCleaner returns the feedbacks account cleaner (order 90).
func NewCleaner(feedbacks repo.Feedbacks) *Cleaner {
	return &Cleaner{feedbacks: feedbacks}
}

// Name implements accountcleanup.Cleaner.
func (*Cleaner) Name() string { return "feedbacks" }

// Order implements accountcleanup.Cleaner.
func (*Cleaner) Order() int { return _cleanerOrder }

// Clean implements accountcleanup.Cleaner.
func (c *Cleaner) Clean(ctx context.Context, userID int) error {
	return c.feedbacks.AnonymizeByUser(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}
