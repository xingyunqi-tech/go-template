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

// Notes implements repo.Notes.
type Notes struct {
	db *DB
}

// NewNotes returns Notes.
func NewNotes(db *DB) *Notes {
	return &Notes{db: db}
}

var _ repo.Notes = (*Notes)(nil)

func noteOf(n *sqlcgen.Note) *entity.Note {
	return &entity.Note{
		ID: n.ID, OwnerSub: n.OwnerSub, Title: n.Title, Content: n.Content, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt,
	}
}

// Create implements repo.Notes.
func (r *Notes) Create(ctx context.Context, n *entity.Note) (*entity.Note, error) {
	var out *entity.Note

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.CreateNote(ctx, sqlcgen.CreateNoteParams{ID: n.ID, OwnerSub: n.OwnerSub, Title: n.Title, Content: n.Content})
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = noteOf(row)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: create note: %w", err)
	}

	return out, nil
}

// one runs a query returning a single note; found is false when it returned no row.
func (r *Notes) one(ctx context.Context, op string, fn func(q *sqlcgen.Queries) (*sqlcgen.Note, error)) (*entity.Note, bool, error) {
	var out *entity.Note

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := fn(q)
		if err != nil {
			return err
		}

		out = noteOf(row)

		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("postgres: %s note: %w", op, err)
	}

	return out, true, nil
}

// Get implements repo.Notes.
func (r *Notes) Get(ctx context.Context, owner, id uuid.UUID) (*entity.Note, bool, error) {
	return r.one(ctx, "get", func(q *sqlcgen.Queries) (*sqlcgen.Note, error) {
		return q.GetNote(ctx, sqlcgen.GetNoteParams{ID: id, OwnerSub: owner}) //nolint:wrapcheck // wrapped by one
	})
}

// Update implements repo.Notes.
func (r *Notes) Update(ctx context.Context, owner, id uuid.UUID, c *entity.NoteChanges) (*entity.Note, bool, error) {
	return r.one(ctx, "update", func(q *sqlcgen.Queries) (*sqlcgen.Note, error) {
		return q.UpdateNote(ctx, sqlcgen.UpdateNoteParams{Title: c.Title, Content: c.Content, ID: id, OwnerSub: owner}) //nolint:wrapcheck // wrapped by one
	})
}

// List implements repo.Notes.
func (r *Notes) List(ctx context.Context, p *repo.NotePage) ([]entity.Note, error) {
	var out []entity.Note

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.ListNotes(ctx, sqlcgen.ListNotesParams{
			OwnerSub: p.Owner, AfterCreatedAt: p.AfterCreatedAt, AfterID: p.AfterID, RowLimit: int32(p.Limit), //nolint:gosec // limits are at most 101
		})
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		out = make([]entity.Note, 0, len(rows))
		for _, row := range rows {
			out = append(out, *noteOf(row))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: list notes: %w", err)
	}

	return out, nil
}

// Delete implements repo.Notes.
func (r *Notes) Delete(ctx context.Context, owner, id uuid.UUID) (bool, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.SoftDeleteNote(ctx, sqlcgen.SoftDeleteNoteParams{ID: id, OwnerSub: owner})

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return false, fmt.Errorf("postgres: delete note: %w", err)
	}

	return n > 0, nil
}

// Purge implements repo.Purger: it removes every note of the owner.
func (r *Notes) Purge(ctx context.Context, owner uuid.UUID) error {
	if err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.PurgeOwnerNotes(ctx, owner) }); err != nil {
		return fmt.Errorf("postgres: purge notes: %w", err)
	}

	return nil
}
