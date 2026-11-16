package v1

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"uuid"

	"github.com/go-chi/chi/v5"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

var _uuidPattern = regexp.MustCompile(`(?i)^(?:[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}` +
	`|00000000-0000-0000-0000-000000000000|ffffffff-ffff-ffff-ffff-ffffffffffff)$`)

// UploadFile handles POST /api/v1/files/upload for every FILE_DRIVER.
func (s *Server) UploadFile(ctx context.Context, _ gen.UploadFileRequestObject) (gen.UploadFileResponseObject, error) {
	upload := reqctx.UploadOf(ctx)
	user := reqctx.Principal(ctx)

	if s.presigned {
		return s.presignedUpload(ctx, upload.Purpose, user.UserID)
	}

	f, err := s.files.Store(ctx, upload.Received, upload.Purpose, user.UserID)
	if err != nil {
		if upload.Sink != nil {
			upload.Sink.Discard(ctx)
		}

		return nil, err //nolint:wrapcheck // usecase errors are HTTP errors
	}

	return gen.UploadFile201JSONResponse(view.Upload{File: view.NewFile(f, s.urls), UploadSignedURL: ""}), nil
}

func (s *Server) presignedUpload(ctx context.Context, purpose file.Purpose, userID int) (gen.UploadFileResponseObject, error) {
	b := body(ctx)
	name, _ := field(b, "fileName")
	size, _ := field(b, "fileSize")

	n, _ := size.(float64)

	f, signed, err := s.files.Presign(ctx, validatorx.ToString(name), n, purpose, userID)
	if err != nil {
		return nil, err //nolint:wrapcheck // usecase errors are HTTP errors
	}

	return gen.UploadFile201JSONResponse(view.Upload{File: view.NewFile(f, s.urls), UploadSignedURL: signed}), nil
}

func pathParam(r *http.Request, name string) string {
	raw := chi.URLParam(r, name)
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}

	return raw
}

// ReadPrivateFile handles GET /api/v1/files/private/:id (nestjs FilesController.readPrivate).
func (s *Server) ReadPrivateFile(w http.ResponseWriter, r *http.Request) error {
	raw := pathParam(r, "id")
	if !_uuidPattern.MatchString(raw) {
		return httperr.New(http.StatusBadRequest, "Validation failed (uuid is expected)")
	}

	id, err := uuid.Parse(strings.ToLower(raw))
	if err != nil {
		return httperr.New(http.StatusBadRequest, "Validation failed (uuid is expected)")
	}

	f, err := s.files.FindAccessible(r.Context(), id, reqctx.Principal(r.Context()).UserID)
	if err != nil {
		return err //nolint:wrapcheck // usecase errors are HTTP errors
	}

	if s.files.Storage().Local() {
		return sendFile(w, r, s.files.Storage().LocalFile(f.Path))
	}

	redirect(w, r, s.files.URL(f)) //nolint:contextcheck // signing an S3 URL is local computation

	return nil
}

// DownloadFile handles GET /api/v1/files/:path of the local driver: public files only.
func (s *Server) DownloadFile(w http.ResponseWriter, r *http.Request) error {
	name := pathParam(r, "path")

	f, err := s.files.FindPublicByName(r.Context(), path.Base(name))
	if err != nil {
		return err //nolint:wrapcheck // usecase errors are HTTP errors
	}

	if path.Base(name) != name {
		return file.ErrNotFound
	}

	return sendFile(w, r, s.files.Storage().LocalFile(f.Path))
}

// sendFile is Express res.sendFile: weak ETag, Last-Modified, Cache-Control, ranges and
// conditional requests.
func sendFile(w http.ResponseWriter, r *http.Request, diskPath string) error {
	fh, err := os.Open(diskPath) //nolint:gosec // the path is a stored random key under the files root
	if err != nil {
		return file.ErrNotFound
	}
	defer fh.Close()

	info, err := fh.Stat()
	if err != nil || info.IsDir() {
		return file.ErrNotFound
	}

	contentType, ok := file.AllMimeTypes()[file.Extension(diskPath)]
	if !ok {
		contentType = "application/octet-stream"
	}

	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "public, max-age=0")
	h.Set("Accept-Ranges", "bytes")
	h.Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))

	etag := fmt.Sprintf(`W/"%x-%x"`, info.Size(), info.ModTime().UnixMilli())
	h.Set("ETag", etag)

	return send(w, r, fh, info.Size(), etag, info.ModTime().Truncate(time.Second))
}

// redirect is Express res.redirect: 302 with a text or HTML body chosen by Accept.
func redirect(w http.ResponseWriter, r *http.Request, target string) {
	h := w.Header()
	h.Set("Location", target)
	h.Set("Vary", "Accept")

	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "text/html") && !strings.Contains(accept, "text/plain") {
		h.Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusFound)

		page := "<!DOCTYPE html><head><title>Found</title></head><body><p>Found. Redirecting to " + html.EscapeString(target) + "</p></body>"
		_, _ = w.Write([]byte(page))

		return
	}

	h.Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusFound)
	_, _ = w.Write([]byte("Found. Redirecting to " + target)) //nolint:gosec // text/plain body, as Express writes it
}
