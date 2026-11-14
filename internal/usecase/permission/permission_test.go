package permission_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/permission"
)

type rows struct {
	calls int
	data  map[int][]string
}

func (r *rows) RolePermissions(context.Context) (map[int][]string, error) {
	r.calls++

	return r.data, nil
}

func TestAdminHasEveryPermission(t *testing.T) {
	t.Parallel()

	s := permission.New(&rows{calls: 0, data: nil}, redis.NewMemory())

	got, err := s.ForRole(t.Context(), entity.RoleAdmin)
	require.NoError(t, err)
	assert.Equal(t, entity.AllPermissions(), got)
}

func TestStoredPermissionsAreCached(t *testing.T) {
	t.Parallel()

	source := &rows{calls: 0, data: map[int][]string{entity.RoleUser: {"a"}}}
	s := permission.New(source, redis.NewMemory())

	for range 3 {
		got, err := s.ForRole(t.Context(), entity.RoleUser)
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, got)
	}

	assert.Equal(t, 1, source.calls)

	got, err := s.ForRole(t.Context(), entity.RoleEditor)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestEmptyTableFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	s := permission.New(&rows{calls: 0, data: map[int][]string{}}, redis.NewMemory())

	got, err := s.ForRole(t.Context(), entity.RoleReviewer)
	require.NoError(t, err)
	assert.Equal(t, entity.DefaultRolePermissions()[entity.RoleReviewer], got)
}
