package postgres

import (
	"context"
	"errors"
	"fmt"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Files implements repo.Files.
type Files struct {
	db *DB
}

// NewFiles returns Files.
func NewFiles(db *DB) *Files {
	return &Files{db: db}
}

var _ repo.Files = (*Files)(nil)

func fileOf(f *sqlcgen.File) entity.File {
	return entity.File{
		ID: f.ID, OwnerSub: f.OwnerSub, Purpose: f.Purpose, Name: f.Name, ContentType: f.ContentType, Size: f.Size,
		SHA256: f.Sha256, Storage: f.Storage, ObjectKey: f.ObjectKey, CreatedAt: f.CreatedAt,
	}
}

func filesOf(rows []*sqlcgen.File) []entity.File {
	out := make([]entity.File, 0, len(rows))
	for _, row := range rows {
		out = append(out, fileOf(row))
	}

	return out
}

// Create implements repo.Files.
func (r *Files) Create(ctx context.Context, f *entity.File) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.CreateFile(ctx, sqlcgen.CreateFileParams{
			ID: f.ID, OwnerSub: f.OwnerSub, Purpose: f.Purpose, Name: f.Name, ContentType: f.ContentType, Size: f.Size,
			Sha256: f.SHA256, Storage: f.Storage, ObjectKey: f.ObjectKey,
		})
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		f.CreatedAt = row.CreatedAt

		return nil
	})
	if err != nil {
		return fmt.Errorf("postgres: create file: %w", err)
	}

	return nil
}

// Get implements repo.Files.
func (r *Files) Get(ctx context.Context, id uuid.UUID) (*entity.File, bool, error) {
	var out entity.File

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetFile(ctx, id)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = fileOf(row)

		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("postgres: get file: %w", err)
	}

	return &out, true, nil
}

// ListByID implements repo.Files.
func (r *Files) ListByID(ctx context.Context, ids []uuid.UUID) ([]entity.File, error) {
	var out []entity.File

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListFilesByID(ctx, ids)
		out = filesOf(rows)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list files: %w", err)
	}

	return out, nil
}

// ListOwner implements repo.Files.
func (r *Files) ListOwner(ctx context.Context, owner uuid.UUID) ([]entity.File, error) {
	var out []entity.File

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListOwnerFiles(ctx, owner)
		out = filesOf(rows)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list owner files: %w", err)
	}

	return out, nil
}

// DeleteOwner implements repo.Files.
func (r *Files) DeleteOwner(ctx context.Context, owner uuid.UUID) error {
	if err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.DeleteOwnerFiles(ctx, owner) }); err != nil {
		return fmt.Errorf("postgres: delete owner files: %w", err)
	}

	return nil
}
