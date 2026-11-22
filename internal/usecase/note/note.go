// Package note is the CRUD example module: notes visible to their owner only.
package note

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

// _cursorLayout keeps the microseconds PostgreSQL stores, so that the next page starts exactly
// after the last note.
const _cursorLayout = time.RFC3339Nano

const _minChanges = 1

// Service implements usecase.Notes.
type Service struct {
	notes repo.Notes
}

var _ usecase.Notes = (*Service)(nil)

// New returns a Service.
func New(notes repo.Notes) *Service {
	return &Service{notes: notes}
}

func notFound(found bool, err error) error {
	if err == nil && !found {
		return entity.New(entity.KeyNotFound)
	}

	return err
}

// List implements usecase.Notes.
func (s *Service) List(ctx context.Context, owner uuid.UUID, cursor string, limit int) (pagination.CursorPage[entity.Note], error) {
	page := &repo.NotePage{Owner: owner, AfterCreatedAt: nil, AfterID: nil, Limit: limit + 1}

	if cursor != "" {
		after, id, err := decodeCursor(cursor)
		if err != nil {
			return pagination.CursorPage[entity.Note]{}, err
		}

		page.AfterCreatedAt, page.AfterID = &after, &id
	}

	rows, err := s.notes.List(ctx, page)
	if err != nil {
		return pagination.CursorPage[entity.Note]{}, err //nolint:wrapcheck // repo errors are wrapped
	}

	return pagination.NewCursorPage(rows, limit, func(n entity.Note) pagination.Cursor {
		return pagination.Cursor{Key: n.CreatedAt.UTC().Format(_cursorLayout), ID: n.ID.String()}
	}), nil
}

func decodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	invalid := entity.Invalid("query.cursor", entity.RuleFormat, map[string]any{"format": "cursor"})

	c, err := pagination.DecodeCursor(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil(), invalid
	}

	after, err := time.Parse(_cursorLayout, c.Key)
	if err != nil {
		return time.Time{}, uuid.Nil(), errors.Join(invalid, err)
	}

	id, err := uuid.Parse(c.ID)
	if err != nil {
		return time.Time{}, uuid.Nil(), errors.Join(invalid, err)
	}

	return after, id, nil
}

// Create implements usecase.Notes.
func (s *Service) Create(ctx context.Context, owner uuid.UUID, title, content string) (*entity.Note, error) {
	return s.notes.Create(ctx, &entity.Note{ //nolint:wrapcheck // repo errors are wrapped
		ID: uuid.NewV7(), OwnerSub: owner, Title: title, Content: content, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	})
}

// Get implements usecase.Notes.
func (s *Service) Get(ctx context.Context, owner, id uuid.UUID) (*entity.Note, error) {
	n, found, err := s.notes.Get(ctx, owner, id)

	return n, notFound(found, err)
}

// Update implements usecase.Notes.
func (s *Service) Update(ctx context.Context, owner, id uuid.UUID, c *entity.NoteChanges) (*entity.Note, error) {
	if c.Title == nil && c.Content == nil {
		return nil, entity.Invalid("body", entity.RuleMinProperties, map[string]any{"minProperties": _minChanges})
	}

	n, found, err := s.notes.Update(ctx, owner, id, c)

	return n, notFound(found, err)
}

// Delete implements usecase.Notes.
func (s *Service) Delete(ctx context.Context, owner, id uuid.UUID) error {
	found, err := s.notes.Delete(ctx, owner, id)

	return notFound(found, err)
}
