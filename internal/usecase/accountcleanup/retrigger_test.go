package accountcleanup_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/accountcleanup"
)

type userStates struct {
	repo.AccountCleanups

	states map[int]bool
}

func (u userStates) UserState(_ context.Context, id int) (exists, removed bool, err error) {
	removed, exists = u.states[id]

	return exists, removed, nil
}

type queued struct {
	repo.Jobs

	ids []int
}

func (q *queued) EnqueueAccountCleanup(_ context.Context, id int) error {
	q.ids = append(q.ids, id)

	return nil
}

func TestRetriggerQueuesOnlyRemovedUsers(t *testing.T) {
	t.Parallel()

	jobs := &queued{}
	s := accountcleanup.New(userStates{states: map[int]bool{1: false, 2: true}}, jobs, nil, slog.New(slog.DiscardHandler))

	require.NoError(t, s.Retrigger(t.Context(), 2))
	assert.Equal(t, []int{2}, jobs.ids)

	assert.Equal(t, entity.Invalid("user", "userNotDeleted"), s.Retrigger(t.Context(), 1))
	assert.Equal(t, entity.NotFoundKey("userNotFound"), s.Retrigger(t.Context(), 3))
	assert.Equal(t, []int{2}, jobs.ids)
}
