package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

func TestAdminHoldsEveryPermissionAndUserNone(t *testing.T) {
	t.Parallel()

	rbac := entity.RBAC{}

	for _, code := range []string{"user:read", "user:write", "role:read", "role:write", "audit:read"} {
		assert.True(t, rbac.Granted([]string{"user", "admin"}, code), code)
		assert.False(t, rbac.Granted([]string{"user"}, code), code)
	}

	assert.False(t, rbac.Granted([]string{"admin"}, "note:delete"))
	assert.Subset(t, entity.Permissions(), []string{"audit:read", "role:read", "role:write", "user:read", "user:write"})
	assert.IsNonDecreasing(t, entity.Permissions())

	roles := entity.Roles()
	assert.Equal(t, "admin", roles[0].Code)
	assert.Equal(t, entity.Permissions(), roles[0].Permissions)
	assert.Empty(t, roles[1].Permissions)
	assert.True(t, entity.KnownRole("user"))
	assert.False(t, entity.KnownRole("owner"))
}
