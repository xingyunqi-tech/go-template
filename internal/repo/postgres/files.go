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
			out = append(out, *fileEntity(f))
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("files: get: %w", err)
	}

	return out, nil
}

func fileEntity(f *sqlcgen.File) *entity.File {
	return &entity.File{
		ID: f.ID, Path: f.Path, Visibility: f.Visibility, MimeType: f.MimeType,
		Size: intPtr(f.Size), OwnerUserID: intPtr(f.OwnerUserId),
	}
}

// Create inserts f.
func (r *Files) Create(ctx context.Context, f *entity.File) (*entity.File, error) {
	var out *entity.File

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.CreateFile(ctx, sqlcgen.CreateFileParams{
			Path: f.Path, OwnerUserID: int32Ptr(f.OwnerUserID), Visibility: f.Visibility, MimeType: f.MimeType, Size: int32Ptr(f.Size),
		})
		if err == nil {
			out = fileEntity(row)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("files: create: %w", err)
	}

	return out, nil
}

func (r *Files) one(ctx context.Context, get func(q *sqlcgen.Queries) (*sqlcgen.File, error)) (*entity.File, error) {
	var out *entity.File

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := get(q)

		switch {
		case notFound(err):
			return nil
		case err != nil:
			return err
		}

		out = fileEntity(row)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("files: find: %w", err)
	}

	return out, nil
}

// Get implements repo.Files.
func (r *Files) Get(ctx context.Context, id uuid.UUID) (*entity.File, error) {
	return r.one(ctx, func(q *sqlcgen.Queries) (*sqlcgen.File, error) { return q.GetFile(ctx, id) })
}

// GetByPath implements repo.Files.
func (r *Files) GetByPath(ctx context.Context, path string) (*entity.File, error) {
	return r.one(ctx, func(q *sqlcgen.Queries) (*sqlcgen.File, error) { return q.GetFileByPath(ctx, path) })
}

// ListByOwner implements repo.Files.
func (r *Files) ListByOwner(ctx context.Context, ownerID int, afterID *uuid.UUID, limit int) ([]entity.File, error) {
	var out []entity.File

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListFilesByOwner(ctx, sqlcgen.ListFilesByOwnerParams{
			OwnerUserID: int32Ptr(&ownerID), AfterID: afterID, Lim: int32(limit), //nolint:gosec // small page sizes
		})
		for _, f := range rows {
			out = append(out, *fileEntity(f))
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("files: list by owner: %w", err)
	}

	return out, nil
}

// Delete implements repo.Files.
func (r *Files) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.DeleteFile(ctx, id) }); err != nil {
		return fmt.Errorf("files: delete: %w", err)
	}

	return nil
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
