package v1

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

func fileOf(v *usecase.FileView) gen.File {
	f := v.File

	return gen.File{
		Id: f.ID, Name: f.Name, Size: f.Size, ContentType: f.ContentType, Purpose: f.Purpose, Url: v.URL,
		CreatedAt: jsontime.New(f.CreatedAt),
	}
}

// UploadFile handles POST /api/v1/files.
func (s *Server) UploadFile(ctx context.Context, req gen.UploadFileRequestObject) (gen.UploadFileResponseObject, error) {
	v, err := s.files.Upload(ctx, caller(ctx).UserID, req.Body)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.UploadFile201JSONResponse{Code: response.Code, Message: response.Message, Data: fileOf(v)}, nil
}

// GetFile handles GET /api/v1/files/{id}.
func (s *Server) GetFile(ctx context.Context, req gen.GetFileRequestObject) (gen.GetFileResponseObject, error) {
	v, err := s.files.Get(ctx, caller(ctx).UserID, req.Id)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return gen.GetFile200JSONResponse{Code: response.Code, Message: response.Message, Data: fileOf(v)}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

// GetFileContent handles GET /api/v1/files/{id}/content.
func (s *Server) GetFileContent(ctx context.Context, req gen.GetFileContentRequestObject) (gen.GetFileContentResponseObject, error) {
	f, body, err := s.files.Content(ctx, req.Id, deref(req.Params.Expires), deref(req.Params.Signature))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are entity errors
	}

	return fileContent{file: f, body: body}, nil
}

// fileContent writes a file with its own type: never sniffed, and saved rather than shown
// unless it is an image.
type fileContent struct {
	file *entity.File
	body io.ReadCloser
}

// VisitGetFileContentResponse implements gen.GetFileContentResponseObject.
func (c fileContent) VisitGetFileContentResponse(w http.ResponseWriter) error {
	defer c.body.Close()

	h := w.Header()
	h.Set("Content-Type", c.file.ContentType)
	h.Set("Content-Length", strconv.FormatInt(c.file.Size, 10))
	h.Set("X-Content-Type-Options", "nosniff")

	if !c.file.Image() {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": c.file.Name}))
	}

	w.WriteHeader(http.StatusOK)

	// The status line is out; a failed copy, such as a client gone away, has nothing left to answer.
	_, _ = io.Copy(w, c.body)

	return nil
}
