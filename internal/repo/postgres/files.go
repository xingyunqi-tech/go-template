package postgres

import (
	"context"
	"fmt"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Files implements repo.Files.
type Files struct {
	db *DB
}

// NewFiles returns the files repository.
func NewFiles(db *DB) *Files {
	return &Files{db: db}
}

var _ repo.Files = (*Files)(nil)

// GetMany returns the files with the given ids that exist.
func (r *Files) GetMany(ctx context.Context, ids []uuid.UUID) ([]entity.File, error) {
	var out []entity.File

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.GetFiles(ctx, ids)
		for _, f := range rows {
			out = append(out, entity.File{
				ID: f.ID, Path: f.Path, Visibility: f.Visibility, MimeType: f.MimeType,
				Size: intPtr(f.Size), OwnerUserID: intPtr(f.OwnerUserId),
			})
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("files: get: %w", err)
	}

	return out, nil
}

// Permissions implements repo.Permissions.
type Permissions struct {
	db *DB
}

// NewPermissions returns the permissions repository.
func NewPermissions(db *DB) *Permissions {
	return &Permissions{db: db}
}

var _ repo.Permissions = (*Permissions)(nil)

// RolePermissions returns role id → permission ids from role_permission.
func (r *Permissions) RolePermissions(ctx context.Context) (map[int][]string, error) {
	out := map[int][]string{}

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListRolePermissions(ctx)
		for _, row := range rows {
			out[int(row.RoleID)] = append(out[int(row.RoleID)], row.PermissionID)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("permissions: list: %w", err)
	}

	return out, nil
}
