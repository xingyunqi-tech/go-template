package feedback_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/mocks"
	"github.com/xingyunqi-tech/go-template/internal/usecase/feedback"
)

var errDB = errors.New("db down")

func newService(t *testing.T) (*feedback.Service, *mocks.MockFeedbacks) {
	t.Helper()

	feedbacks := mocks.NewMockFeedbacks(gomock.NewController(t))

	return feedback.New(feedbacks), feedbacks
}

func ptr[T any](v T) *T { return &v }

func TestCreateStoresSubmissionOfUser(t *testing.T) {
	t.Parallel()

	svc, feedbacks := newService(t)
	in := &feedback.Input{Type: entity.FeedbackBug, Content: "broken", Images: []byte(`["a.png"]`), Contact: ptr("me@example.com")}
	want := &entity.Feedback{ID: 7}

	feedbacks.EXPECT().Create(gomock.Any(), &repo.NewFeedback{
		UserID: 3, Type: in.Type, Content: in.Content, Images: in.Images, Contact: in.Contact,
	}).Return(want, nil)

	got, err := svc.Create(context.Background(), 3, in)
	require.NoError(t, err)
	assert.Same(t, want, got)
}

func TestCreateReturnsRepoError(t *testing.T) {
	t.Parallel()

	svc, feedbacks := newService(t)
	feedbacks.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil, errDB)

	_, err := svc.Create(context.Background(), 3, &feedback.Input{})
	require.ErrorIs(t, err, errDB)
}

func TestListDefaultsToGeneralTypesAndSkipsCountOnPartialPage(t *testing.T) {
	t.Parallel()

	svc, feedbacks := newService(t)
	filter := &repo.FeedbackFilter{UserID: 3, Types: entity.GeneralFeedbackTypes(), Status: "pending"}
	feedbacks.EXPECT().List(gomock.Any(), filter, int64(10), int64(10)).Return(make([]entity.Feedback, 2), nil)

	data, total, err := svc.List(context.Background(), &feedback.List{UserID: 3, Status: "pending", Page: 2, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, data, 2)
	assert.InDelta(t, 12, total, 0)
}

func TestListCountsWhenPageIsFull(t *testing.T) {
	t.Parallel()

	svc, feedbacks := newService(t)
	filter := &repo.FeedbackFilter{UserID: 3, Types: []string{entity.FeedbackQuestionError}}
	feedbacks.EXPECT().List(gomock.Any(), filter, int64(0), int64(2)).Return(make([]entity.Feedback, 2), nil)
	feedbacks.EXPECT().Count(gomock.Any(), filter).Return(5, nil)

	_, total, err := svc.List(context.Background(), &feedback.List{UserID: 3, Type: entity.FeedbackQuestionError, Page: 1, Limit: 2})
	require.NoError(t, err)
	assert.InDelta(t, 5, total, 0)
}

func TestListReturnsRepoErrors(t *testing.T) {
	t.Parallel()

	t.Run("list", func(t *testing.T) {
		t.Parallel()

		svc, feedbacks := newService(t)
		feedbacks.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errDB)

		_, _, err := svc.List(context.Background(), &feedback.List{Page: 1, Limit: 10})
		require.ErrorIs(t, err, errDB)
	})

	t.Run("count", func(t *testing.T) {
		t.Parallel()

		svc, feedbacks := newService(t)
		feedbacks.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(make([]entity.Feedback, 1), nil)
		feedbacks.EXPECT().Count(gomock.Any(), gomock.Any()).Return(0, errDB)

		_, _, err := svc.List(context.Background(), &feedback.List{Page: 1, Limit: 1})
		require.ErrorIs(t, err, errDB)
	})
}

func TestGetReturnsOwnQueryableFeedback(t *testing.T) {
	t.Parallel()

	svc, feedbacks := newService(t)
	want := &entity.Feedback{ID: 7, UserID: ptr(3), Type: entity.FeedbackQuestionError}
	feedbacks.EXPECT().Get(gomock.Any(), 7).Return(want, nil)

	got, err := svc.Get(context.Background(), 7, 3)
	require.NoError(t, err)
	assert.Same(t, want, got)
}

func TestGetHidesFeedbackOutsideTheUsersView(t *testing.T) {
	t.Parallel()

	cases := map[string]*entity.Feedback{
		"missing":       nil,
		"anonymized":    {ID: 7, Type: entity.FeedbackBug},
		"other user":    {ID: 7, UserID: ptr(4), Type: entity.FeedbackBug},
		"not queryable": {ID: 7, UserID: ptr(3), Type: "internal"},
	}

	for name, row := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, feedbacks := newService(t)
			feedbacks.EXPECT().Get(gomock.Any(), 7).Return(row, nil)

			_, err := svc.Get(context.Background(), 7, 3)

			var e *entity.Error
			require.ErrorAs(t, err, &e)
			assert.Equal(t, entity.StatusNotFound, e.Status)
			assert.Equal(t, "feedbackNotFound", e.ErrorKey)
		})
	}
}

func TestGetReturnsRepoError(t *testing.T) {
	t.Parallel()

	svc, feedbacks := newService(t)
	feedbacks.EXPECT().Get(gomock.Any(), 7).Return(nil, errDB)

	_, err := svc.Get(context.Background(), 7, 3)
	require.ErrorIs(t, err, errDB)
}

func TestCleanerAnonymizesFeedbackOfUser(t *testing.T) {
	t.Parallel()

	feedbacks := mocks.NewMockFeedbacks(gomock.NewController(t))
	feedbacks.EXPECT().AnonymizeByUser(gomock.Any(), 3).Return(errDB)

	c := feedback.NewCleaner(feedbacks)
	assert.Equal(t, "feedbacks", c.Name())
	assert.Equal(t, 90, c.Order())
	require.ErrorIs(t, c.Clean(context.Background(), 3), errDB)
}
