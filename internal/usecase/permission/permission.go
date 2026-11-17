// Package permission resolves role permissions with a five-minute cache, falling back to the
// nestjs default mapping when role_permission is empty.
package permission

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
)

// CacheKey is the cached role permission map.
const CacheKey = "permissions:v2:role-map"

const _cacheTTL = 5 * time.Minute

// Service resolves permissions.
type Service struct {
	repo  repo.Permissions
	cache repo.Cache
}

// New returns a Service.
func New(permissions repo.Permissions, cache repo.Cache) *Service {
	return &Service{repo: permissions, cache: cache}
}

// ForRole returns the role's permissions; admin has all of them.
func (s *Service) ForRole(ctx context.Context, roleID int) ([]string, error) {
	if roleID == entity.RoleAdmin {
		return entity.AllPermissions(), nil
	}

	var roleMap map[string][]string

	found, err := s.cache.Get(ctx, CacheKey, &roleMap)
	if err != nil {
		return nil, fmt.Errorf("permission: cache: %w", err)
	}

	if !found || len(roleMap) == 0 {
		if roleMap, err = s.load(ctx); err != nil {
			return nil, err
		}
	}

	return roleMap[strconv.Itoa(roleID)], nil
}

func (s *Service) load(ctx context.Context) (map[string][]string, error) {
	stored, err := s.repo.RolePermissions(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if len(stored) == 0 {
		stored = entity.DefaultRolePermissions()
	}

	roleMap := make(map[string][]string, len(stored))
	for id, perms := range stored {
		roleMap[strconv.Itoa(id)] = perms
	}

	if err := s.cache.Set(ctx, CacheKey, roleMap, _cacheTTL); err != nil {
		return nil, fmt.Errorf("permission: cache: %w", err)
	}

	return roleMap, nil
}
