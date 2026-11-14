package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/feedback"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// jsonField is the JSON of a present, non-null field, or nil.
func jsonField(o *validatorx.Object, key string) []byte {
	v, ok := field(o, key)
	if !ok || v == nil {
		return nil
	}

	s, _ := validatorx.Stringify(v)

	return []byte(s)
}

// CreateFeedback handles POST feedbacks.
func (s *Server) CreateFeedback(ctx context.Context, _ gen.CreateFeedbackRequestObject) (gen.CreateFeedbackResponseObject, error) {
	b := body(ctx)

	f, err := s.feedbacks.Create(ctx, callerID(ctx), &feedback.Input{
		Type: truthyString(b, "type"), Content: truthyString(b, "content"),
		Images: jsonField(b, "images"), Contact: stringPtr(b, "contact"),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.CreateFeedback201JSONResponse(view.NewFeedback(f)), nil
}

// GetFeedbacks handles GET feedbacks.
func (s *Server) GetFeedbacks(ctx context.Context, _ gen.GetFeedbacksRequestObject) (gen.GetFeedbacksResponseObject, error) {
	q := query(ctx)
	page, limit := pageAndLimit(q)

	data, total, err := s.feedbacks.List(ctx, &feedback.List{
		UserID: callerID(ctx), Type: truthyString(q, "type"), Status: truthyString(q, "status"), Page: page, Limit: limit,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	items := make([]view.Feedback, len(data))
	for i := range data {
		items[i] = view.NewFeedback(&data[i])
	}

	return gen.GetFeedbacks200JSONResponse(pagination.New(items, total, page, limit)), nil
}

// GetFeedback handles GET feedbacks/:id.
func (s *Server) GetFeedback(ctx context.Context, req gen.GetFeedbackRequestObject) (gen.GetFeedbackResponseObject, error) {
	f, err := s.feedbacks.Get(ctx, int(req.Id), callerID(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetFeedback200JSONResponse(view.NewFeedback(f)), nil
}
