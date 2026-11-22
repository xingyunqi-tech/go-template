package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

func noteOf(n *entity.Note) gen.Note {
	return gen.Note{
		Id: n.ID, Title: n.Title, Content: n.Content, CreatedAt: jsontime.New(n.CreatedAt), UpdatedAt: jsontime.New(n.UpdatedAt),
	}
}

func noteEnvelope(n *entity.Note) gen.NoteEnvelope {
	return gen.NoteEnvelope{Code: response.Code, Message: response.Message, Data: noteOf(n)}
}

// ListNotes handles GET /api/v1/notes.
func (s *Server) ListNotes(ctx context.Context, req gen.ListNotesRequestObject) (gen.ListNotesResponseObject, error) {
	cursor, limit := "", pagination.DefaultSize

	if req.Params.Cursor != nil {
		cursor = *req.Params.Cursor
	}

	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}

	page, err := s.notes.List(ctx, reqctx.PrincipalOf(ctx).Subject, cursor, limit)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	items := make([]gen.Note, 0, len(page.Items))
	for i := range page.Items {
		items = append(items, noteOf(&page.Items[i]))
	}

	return gen.ListNotes200JSONResponse{
		Code: response.Code, Message: response.Message, Data: gen.NotePage{Items: items, NextCursor: page.NextCursor},
	}, nil
}

// CreateNote handles POST /api/v1/notes.
func (s *Server) CreateNote(ctx context.Context, req gen.CreateNoteRequestObject) (gen.CreateNoteResponseObject, error) {
	content := ""
	if req.Body.Content != nil {
		content = *req.Body.Content
	}

	n, err := s.notes.Create(ctx, reqctx.PrincipalOf(ctx).Subject, req.Body.Title, content)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.CreateNote201JSONResponse(noteEnvelope(n)), nil
}

// GetNote handles GET /api/v1/notes/{id}.
func (s *Server) GetNote(ctx context.Context, req gen.GetNoteRequestObject) (gen.GetNoteResponseObject, error) {
	n, err := s.notes.Get(ctx, reqctx.PrincipalOf(ctx).Subject, req.Id)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.GetNote200JSONResponse(noteEnvelope(n)), nil
}

// UpdateNote handles PATCH /api/v1/notes/{id}.
func (s *Server) UpdateNote(ctx context.Context, req gen.UpdateNoteRequestObject) (gen.UpdateNoteResponseObject, error) {
	n, err := s.notes.Update(ctx, reqctx.PrincipalOf(ctx).Subject, req.Id, &entity.NoteChanges{
		Title: req.Body.Title, Content: req.Body.Content,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.UpdateNote200JSONResponse(noteEnvelope(n)), nil
}

// DeleteNote handles DELETE /api/v1/notes/{id}.
func (s *Server) DeleteNote(ctx context.Context, req gen.DeleteNoteRequestObject) (gen.DeleteNoteResponseObject, error) {
	if err := s.notes.Delete(ctx, reqctx.PrincipalOf(ctx).Subject, req.Id); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.DeleteNote200JSONResponse{Code: response.Code, Message: response.Message, Data: nil}, nil
}
