// Package usecase declares the use cases the HTTP handlers depend on.
package usecase

import (
	"context"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// Notes manages the notes of the calling user.
type Notes interface {
	// List returns a page after cursor ("" for the first page) of at most limit notes.
	List(ctx context.Context, owner uuid.UUID, cursor string, limit int) (pagination.CursorPage[entity.Note], error)
	Create(ctx context.Context, owner uuid.UUID, title, content string) (*entity.Note, error)
	Get(ctx context.Context, owner, id uuid.UUID) (*entity.Note, error)
	Update(ctx context.Context, owner, id uuid.UUID, c *entity.NoteChanges) (*entity.Note, error)
	Delete(ctx context.Context, owner, id uuid.UUID) error
}
