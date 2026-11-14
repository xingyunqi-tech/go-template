package http

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/httperr"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_multipartOverhead  = 16 * 1024
	_fileField          = "file"
	_fieldSizeLimit     = 1024 * 1024
	_fieldNestingDepth  = 32
	_fieldArrayMaxIndex = 1000
	_readChunk          = 32 * 1024
	_presignedDTO       = "FileUploadDto"
	_closingDash        = "--"
)

var (
	_bracketPath = regexp.MustCompile(`^[^\[]+(?:\[[^\]]+\])*(?:\[\])?$`)
	_arrayIndex  = regexp.MustCompile(`\[(\d+)\]`)

	errUnexpectedEnd   = httperr.New(http.StatusBadRequest, "Multipart: Unexpected end of form")
	errMalformedHeader = httperr.New(http.StatusBadRequest, "Multipart: Malformed part header")
)

// UploadOptions configures the upload endpoint; nil Files leaves it unimplemented.
type UploadOptions struct {
	Files       *file.Service
	Permissions PermissionSource
	// Presigned is the s3-presigned driver: a JSON FileUploadDto instead of a multipart body.
	Presigned bool
}

// prepareUpload runs what nestjs runs before the upload handler: the purpose guard, then multer
// (or the FileUploadDto body), then the query DTO.
func (p *pipeline) prepareUpload(r *http.Request, policy *Policy) (*http.Request, error) {
	purpose, err := p.uploadGuard(r)
	if err != nil {
		return r, err
	}

	upload := &reqctx.Upload{Purpose: purpose, Received: nil, Sink: nil}
	validated := &reqctx.Validated{Body: validatorx.Undefined{}, Query: validatorx.Undefined{}}

	if p.uploads.Presigned {
		if validated.Body, err = p.validateBody(r, _presignedDTO); err != nil {
			return r, err
		}
	} else if upload.Received, upload.Sink, err = p.receive(r, purpose); err != nil {
		return r, err
	}

	if validated.Query, err = p.validateQuery(r, policy.Query); err != nil {
		if upload.Sink != nil {
			upload.Sink.Discard(r.Context())
		}

		return r, err
	}

	ctx := reqctx.WithValidated(r.Context(), validated)

	return r.WithContext(reqctx.WithUpload(ctx, upload)), nil
}

// uploadGuard is nestjs FileUploadPurposeGuard.
func (p *pipeline) uploadGuard(r *http.Request) (file.Purpose, error) {
	values, present := queryValues(r.URL.RawQuery, "purpose")

	purpose, ok := file.ParsePurpose(values, present)
	if !ok {
		return "", file.ErrInvalidPurpose
	}

	if perm := file.Rules()[purpose].Permission; perm != "" {
		if err := p.requirePermission(r.Context(), perm); err != nil {
			return "", err
		}
	}

	if length, ok := jsNumber(r.Header.Get("Content-Length")); ok && length > float64(p.uploads.Files.MaxSize(purpose)+_multipartOverhead) {
		return "", entity.FileTooLarge()
	}

	return purpose, nil
}

func (p *pipeline) requirePermission(ctx context.Context, perm string) error {
	missing := entity.Forbidden("Missing permissions: " + perm)

	principal := reqctx.Principal(ctx)
	if principal == nil || principal.RoleID == nil {
		return missing
	}

	granted, err := p.uploads.Permissions.ForRole(ctx, *principal.RoleID)
	if err != nil {
		return err //nolint:wrapcheck // permission errors are wrapped
	}

	for _, g := range granted {
		if g == perm {
			return nil
		}
	}

	return missing
}

// queryValues returns the values of key as Node querystring parses them.
func queryValues(rawQuery, key string) ([]string, bool) {
	v, ok := parseQueryString(rawQuery).Get(key)
	if !ok {
		return nil, false
	}

	if arr, isArray := v.(*validatorx.Array); isArray {
		out := make([]string, len(arr.Items))
		for i, item := range arr.Items {
			out[i] = validatorx.ToString(item)
		}

		return out, true
	}

	return []string{validatorx.ToString(v)}, true
}

// jsNumber is Number(header) for a present header: trimmed decimal, empty is 0.
func jsNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, true
	}

	f, err := strconv.ParseFloat(s, 64)

	return f, err == nil
}

// receive is multer FileInterceptor('file') over busboy: only one file in the "file" field, its
// type checked when its header arrives, and the size limit enforced while streaming.
func (p *pipeline) receive(r *http.Request, purpose file.Purpose) (*file.Received, repo.Sink, error) {
	contentType := r.Header.Get("Content-Type")

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "multipart/") {
		return nil, nil, nil //nolint:nilerr // not a multipart body: no file
	}

	if !strings.EqualFold(mediaType, "multipart/form-data") {
		return nil, nil, httperr.New(http.StatusUnsupportedMediaType, "Unsupported content type")
	}

	boundary := params["boundary"]
	if boundary == "" {
		return nil, nil, httperr.New(http.StatusBadRequest, "Multipart: Boundary not found")
	}

	rc := &receiver{
		files: p.uploads.Files, purpose: purpose, max: p.uploads.Files.MaxFileSize(), received: nil, sink: nil,
	}

	if err := rc.read(r.Context(), r.Body, boundary); err != nil {
		if rc.sink != nil {
			rc.sink.Discard(r.Context())
		}

		return nil, nil, err
	}

	return rc.received, rc.sink, nil
}

type receiver struct {
	files    *file.Service
	purpose  file.Purpose
	max      int64
	received *file.Received
	sink     repo.Sink
}

func (rc *receiver) read(ctx context.Context, body io.Reader, boundary string) error {
	br := bufio.NewReaderSize(body, _readChunk)
	delimiter := []byte("--" + boundary)

	if err := skipPreamble(br, delimiter); err != nil {
		return err
	}

	for {
		final, err := readBoundaryRest(br)
		if err != nil {
			return err
		}

		if final {
			return nil
		}

		if err := rc.part(ctx, br, delimiter); err != nil {
			return err
		}
	}
}

// skipPreamble consumes everything up to and including the first delimiter.
func skipPreamble(br *bufio.Reader, delimiter []byte) error {
	marker := append([]byte("\r\n"), delimiter...)
	tail := make([]byte, 0, len(marker)+1)

	for n := 1; ; n++ {
		b, err := br.ReadByte()
		if err != nil {
			return errUnexpectedEnd
		}

		tail = append(tail, b)
		if len(tail) > len(marker) {
			tail = tail[1:]
		}

		if (n == len(delimiter) && bytes.Equal(tail, delimiter)) || bytes.HasSuffix(tail, marker) {
			return nil
		}
	}
}

// readBoundaryRest reads what follows a delimiter: "--" ends the form, CRLF starts a part.
func readBoundaryRest(br *bufio.Reader) (bool, error) {
	two, err := br.Peek(len(_closingDash))
	if err != nil {
		return false, errUnexpectedEnd
	}

	if string(two) == _closingDash {
		return true, nil
	}

	line, err := br.ReadString('\n')
	if err != nil {
		return false, errUnexpectedEnd
	}

	if strings.TrimRight(line, " \t\r\n") != "" {
		return false, errMalformedHeader
	}

	return false, nil
}

func (rc *receiver) part(ctx context.Context, br *bufio.Reader, delimiter []byte) error {
	header, err := textproto.NewReader(br).ReadMIMEHeader()
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return errUnexpectedEnd
		}

		return errMalformedHeader
	}

	kind, name, sink, err := rc.classify(header)
	if err != nil {
		return err
	}

	pw := &partWriter{kind: kind, name: name, sink: sink, max: rc.max, size: 0}
	if err := copyPart(br, delimiter, pw.write); err != nil {
		return err
	}

	switch kind {
	case "field":
		return checkFieldName(name)
	case _fileField:
		rc.received.Size = pw.size

		return sink.Commit(ctx) //nolint:wrapcheck // storage error
	}

	return nil
}

// partWriter enforces the field and file limits while a part streams.
type partWriter struct {
	kind, name string
	sink       repo.Sink
	max        int64
	size       int64
}

func (pw *partWriter) write(chunk []byte) error {
	pw.size += int64(len(chunk))

	switch pw.kind {
	case "field":
		if pw.size > _fieldSizeLimit {
			return httperr.New(http.StatusBadRequest, "Field value too long - "+pw.name)
		}
	case _fileField:
		if pw.size > pw.max {
			return entity.FileTooLarge()
		}

		if _, err := pw.sink.Write(chunk); err != nil {
			return err //nolint:wrapcheck // storage error
		}
	}

	return nil
}

// classify reads Content-Disposition: text fields, the file, or parts that are skipped.
func (rc *receiver) classify(header textproto.MIMEHeader) (kind, name string, sink repo.Sink, err error) {
	disposition := header.Get("Content-Disposition")
	if disposition == "" {
		return "skip", "", nil, nil
	}

	dispositionType, params, parseErr := mime.ParseMediaType(disposition)
	if parseErr != nil || dispositionType != "form-data" {
		return "skip", "", nil, nil //nolint:nilerr // busboy ignores such parts
	}

	name, ok := params["name"]
	if !ok {
		return "", "", nil, httperr.New(http.StatusBadRequest, "Field name missing")
	}

	filename, isFile := params["filename"]
	if !isFile {
		return "field", name, nil, nil
	}

	if i := strings.LastIndexAny(filename, `/\`); i >= 0 {
		filename = filename[i+1:]
	}

	if filename == "" {
		return "skip", name, nil, nil
	}

	sink, err = rc.openFile(name, filename)
	if err != nil {
		return "", "", nil, err
	}

	return _fileField, name, sink, nil
}

// openFile accepts the single "file" upload when its type is allowed for the purpose.
func (rc *receiver) openFile(field, filename string) (repo.Sink, error) {
	if field != _fileField || rc.received != nil {
		return nil, httperr.New(http.StatusBadRequest, "Unexpected file field - "+field)
	}

	mimeType, allowed := file.MimeType(rc.purpose, filename)
	if !allowed {
		return nil, file.ErrFileType
	}

	key := file.RandomKey(filename)

	sink, err := rc.files.Storage().NewSink(key, mimeType)
	if err != nil {
		return nil, err //nolint:wrapcheck // storage error
	}

	rc.sink = sink
	rc.received = &file.Received{Name: filename, Size: 0, Key: key}

	return sink, nil
}

// copyPart streams the part body to emit until CRLF + delimiter, which it consumes.
func copyPart(br *bufio.Reader, delimiter []byte, emit func([]byte) error) error {
	marker := append([]byte("\r\n"), delimiter...)

	for {
		buf, err := br.Peek(_readChunk)

		if i := bytes.Index(buf, marker); i >= 0 {
			if err := emitDiscard(br, buf[:i], emit); err != nil {
				return err
			}

			_, _ = br.Discard(len(marker))

			return nil
		}

		if err != nil {
			return errUnexpectedEnd
		}

		if err := emitDiscard(br, buf[:len(buf)-len(marker)+1], emit); err != nil {
			return err
		}
	}
}

func emitDiscard(br *bufio.Reader, chunk []byte, emit func([]byte) error) error {
	if len(chunk) == 0 {
		return nil
	}

	data := bytes.Clone(chunk)
	_, _ = br.Discard(len(chunk))

	return emit(data)
}

// checkFieldName is the multer text field name check under the default limits.
func checkFieldName(name string) error {
	if strings.Count(name, "[") > _fieldNestingDepth {
		return httperr.New(http.StatusBadRequest, "Field name nesting too deep - "+name)
	}

	if _bracketPath.MatchString(name) {
		for _, m := range _arrayIndex.FindAllStringSubmatch(name, -1) {
			if n, err := strconv.Atoi(m[1]); err != nil || n > _fieldArrayMaxIndex {
				return httperr.New(http.StatusBadRequest, "Field name array index too large - "+name)
			}
		}
	}

	return nil
}
