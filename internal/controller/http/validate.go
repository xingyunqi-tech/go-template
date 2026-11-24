package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/xingyunqi-tech/go-template/internal/entity"
)

// _bodyField names the request body in a field error about the body as a whole.
const _bodyField = "body"

const _jsonMediaType = "application/json"

var (
	errNotEmail = errors.New("not an email address")
	errNotURI   = errors.New("not an absolute URI")
)

func init() { //nolint:gochecknoinits // kin-openapi keeps format validators in a global registry
	RegisterFormats()
}

// RegisterFormats adds the email, uri and uuid formats, which kin-openapi otherwise accepts
// without checking.
func RegisterFormats() {
	openapi3.DefineStringFormatValidator("email", openapi3.NewCallbackValidator(func(s string) error {
		addr, err := mail.ParseAddress(s)
		if err != nil || addr.Address != s || addr.Name != "" {
			return errNotEmail
		}

		return nil
	}))
	openapi3.DefineStringFormatValidator("uri", openapi3.NewCallbackValidator(func(s string) error {
		u, err := url.Parse(s)
		if err != nil || u.Scheme == "" || (u.Host == "" && u.Opaque == "") {
			return errNotURI
		}

		return nil
	}))
	openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC9562))
}

type bodyKey struct{}

// bodyOf returns the request body read by the pipeline.
func bodyOf(ctx context.Context) []byte {
	b, _ := ctx.Value(bodyKey{}).([]byte)

	return b
}

// readBody reads the body of an operation with a request body within its limit and checks its
// Content-Type and JSON syntax.
func (p *pipeline) readBody(r *http.Request, op *operation) (*http.Request, error) {
	if op.mediaTypes == nil || r.Body == nil || r.Body == http.NoBody {
		return r, nil
	}

	limit := p.opts.BodyLimit
	if op.policy.BodyLimit > 0 {
		limit = op.policy.BodyLimit
	}

	if op.policy.Upload {
		return streamBody(r, op, limit)
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return r, entity.Wrap(entity.KeyBadRequest, err)
	}

	if int64(len(body)) > limit {
		return r, entity.New(entity.KeyPayloadTooLarge)
	}

	if len(body) == 0 {
		return r, nil
	}

	if err := checkMediaType(r, op, body); err != nil {
		return r, err
	}

	r = r.WithContext(context.WithValue(r.Context(), bodyKey{}, body))
	r.Body = io.NopCloser(bytes.NewReader(body))

	return r, nil
}

// streamBody hands an upload body over unread, failing reads past limit.
func streamBody(r *http.Request, op *operation, limit int64) (*http.Request, error) {
	if err := checkMediaType(r, op, nil); err != nil {
		return r, err
	}

	r.Body = &uploadBody{ReadCloser: r.Body, left: limit}

	return r, nil
}

// uploadBody fails with file.tooLarge once more than left bytes are read.
type uploadBody struct {
	io.ReadCloser

	left int64
}

func (b *uploadBody) Read(p []byte) (int, error) {
	if b.left < 0 {
		return 0, entity.New(entity.KeyFileTooLarge)
	}

	if int64(len(p)) > b.left+1 {
		p = p[:b.left+1]
	}

	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)

	if b.left < 0 {
		return n, entity.New(entity.KeyFileTooLarge)
	}

	return n, err //nolint:wrapcheck // body errors
}

// checkMediaType accepts a Content-Type of the operation and, for JSON, a well-formed body.
func checkMediaType(r *http.Request, op *operation, body []byte) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !slices.Contains(op.mediaTypes, mediaType) {
		return entity.New(entity.KeyUnsupportedMediaType)
	}

	if mediaType == _jsonMediaType && !json.Valid(body) {
		return entity.New(entity.KeyMalformedJSON)
	}

	return nil
}

// validate checks the path, query and body of r against the operation schema; an upload is
// checked without its body.
func (p *pipeline) validate(r *http.Request, op *operation, params map[string]string) error {
	checked := r
	if op.policy.Upload {
		checked = r.Clone(r.Context())
		checked.Body = http.NoBody
	}

	input := &openapi3filter.RequestValidationInput{ //nolint:exhaustruct // defaults
		Request:    checked,
		PathParams: params,
		Route:      op.route,
		Options: &openapi3filter.Options{ //nolint:exhaustruct // defaults
			MultiError:         true,
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			ExcludeRequestBody: op.policy.Upload,
		},
	}

	err := openapi3filter.ValidateRequest(r.Context(), input)

	if body := bodyOf(r.Context()); body != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	if err == nil {
		return nil
	}

	fields := FieldErrors(err)
	if len(fields) == 0 {
		return entity.Wrap(entity.KeyBadRequest, err)
	}

	return &entity.ValidationError{Errors: fields}
}

// FieldErrors turns the errors of openapi3filter.ValidateRequest into one entry per failed field,
// the first constraint of each field.
func FieldErrors(err error) []entity.FieldError {
	var out []entity.FieldError

	collect(err, "", &out)

	seen := map[string]bool{}
	unique := out[:0]

	for _, f := range out {
		if !seen[f.Field] {
			seen[f.Field] = true
			unique = append(unique, f)
		}
	}

	slices.SortStableFunc(unique, func(a, b entity.FieldError) int { return strings.Compare(a.Field, b.Field) })

	return unique
}

func collect(err error, base string, out *[]entity.FieldError) {
	switch e := err.(type) { //nolint:errorlint // the concrete type decides how the tree is walked
	case openapi3.MultiError:
		for _, inner := range e {
			collect(inner, base, out)
		}
	case *openapi3filter.RequestError:
		collectRequest(e, out)
	case *openapi3.SchemaError:
		collectSchema(e, base, out)
	case *openapi3filter.ParseError:
		*out = append(*out, entity.FieldError{Field: base, Rule: entity.RuleType, Params: nil})
	default:
		if inner := errors.Unwrap(err); inner != nil {
			collect(inner, base, out)
		}
	}
}

func collectRequest(e *openapi3filter.RequestError, out *[]entity.FieldError) {
	base := ""

	if p := e.Parameter; p != nil {
		base = p.In + "." + p.Name
	}

	if errors.Is(e.Err, openapi3filter.ErrInvalidRequired) || errors.Is(e.Err, openapi3filter.ErrInvalidEmptyValue) {
		field := base
		if field == "" {
			field = _bodyField
		}

		*out = append(*out, entity.FieldError{Field: field, Rule: entity.RuleRequired, Params: nil})

		return
	}

	before := len(*out)
	collect(e.Err, base, out)

	if len(*out) == before && base != "" {
		*out = append(*out, entity.FieldError{Field: base, Rule: entity.RuleType, Params: nil})
	}
}

func collectSchema(e *openapi3.SchemaError, base string, out *[]entity.FieldError) {
	var nested openapi3.MultiError
	if e.Origin != nil && errors.As(e.Origin, &nested) {
		for _, n := range nested {
			collect(n, base, out)
		}

		return
	}

	var inner *openapi3.SchemaError
	if e.Origin != nil && errors.As(e.Origin, &inner) {
		collectSchema(inner, base, out)

		return
	}

	field := joinField(base, e.JSONPointer())

	if e.SchemaField == "required" {
		for _, name := range missing(e) {
			*out = append(*out, entity.FieldError{Field: joinField(field, []string{name}), Rule: entity.RuleRequired, Params: nil})
		}

		return
	}

	if field == "" {
		field = _bodyField
	}

	rule, params := ruleOf(e)
	*out = append(*out, entity.FieldError{Field: field, Rule: rule, Params: params})
}

// missing names the absent properties of a required error.
func missing(e *openapi3.SchemaError) []string {
	var verr *jsonschema.ValidationError
	if errors.As(e, &verr) {
		if req, ok := verr.ErrorKind.(*kind.Required); ok {
			return req.Missing
		}
	}

	_, name, ok := strings.Cut(e.Reason, `property "`)
	if ok {
		name, _, _ = strings.Cut(name, `"`)

		return []string{name}
	}

	return []string{""}
}

// joinField writes a JSON pointer below base as dotted names and [index] segments.
func joinField(base string, pointer []string) string {
	var b strings.Builder

	b.WriteString(base)

	for _, token := range pointer {
		if token == "" {
			continue
		}

		if _, err := strconv.Atoi(token); err == nil {
			b.WriteString("[" + token + "]")

			continue
		}

		if b.Len() > 0 {
			b.WriteByte('.')
		}

		b.WriteString(token)
	}

	return b.String()
}

// keywordRules maps the JSON Schema keywords with a fixed rule to the rule and its params.
func keywordRules() map[string]func(s *openapi3.Schema) (string, map[string]any) {
	return map[string]func(s *openapi3.Schema) (string, map[string]any){
		"type": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleType, map[string]any{"type": typeName(s)}
		},
		"format": func(s *openapi3.Schema) (string, map[string]any) { return formatRule(s.Format) },
		"minimum": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMin, map[string]any{"min": deref(s.Min)}
		},
		"exclusiveMinimum": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMin, map[string]any{"min": bound(s.ExclusiveMin, s.Min), "exclusive": true}
		},
		"maximum": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMax, map[string]any{"max": deref(s.Max)}
		},
		"exclusiveMaximum": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMax, map[string]any{"max": bound(s.ExclusiveMax, s.Max), "exclusive": true}
		},
		"minLength": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMinLength, map[string]any{"minLength": s.MinLength}
		},
		"maxLength": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMaxLength, map[string]any{"maxLength": derefUint(s.MaxLength)}
		},
		"minProperties": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMinProperties, map[string]any{"minProperties": s.MinProps}
		},
		"minItems": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMinItems, map[string]any{"minItems": s.MinItems}
		},
		"maxItems": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleMaxItems, map[string]any{"maxItems": derefUint(s.MaxItems)}
		},
		"pattern": func(*openapi3.Schema) (string, map[string]any) { return entity.RulePattern, nil },
		"enum": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleEnum, map[string]any{"values": s.Enum}
		},
		"const": func(s *openapi3.Schema) (string, map[string]any) {
			return entity.RuleEnum, map[string]any{"values": []any{s.Const}}
		},
	}
}

func ruleOf(e *openapi3.SchemaError) (rule string, params map[string]any) {
	s := e.Schema
	if s == nil {
		s = &openapi3.Schema{} //nolint:exhaustruct // empty schema
	}

	if fn, ok := keywordRules()[e.SchemaField]; ok {
		return fn(s)
	}

	return e.SchemaField, nil
}

func formatRule(format string) (rule string, params map[string]any) {
	switch format {
	case "email":
		return entity.RuleEmail, nil
	case "uri":
		return entity.RuleURL, nil
	case "uuid":
		return entity.RuleUUID, nil
	default:
		return entity.RuleFormat, map[string]any{"format": format}
	}
}

func typeName(s *openapi3.Schema) any {
	if s.Type == nil {
		return nil
	}

	types := slices.DeleteFunc(slices.Clone(s.Type.Slice()), func(t string) bool { return t == openapi3.TypeNull })
	if len(types) == 1 {
		return types[0]
	}

	return types
}

func deref(v *float64) any {
	if v == nil {
		return nil
	}

	return *v
}

func derefUint(v *uint64) any {
	if v == nil {
		return nil
	}

	return *v
}

func bound(b openapi3.ExclusiveBound, legacy *float64) any {
	if b.Value != nil {
		return *b.Value
	}

	return deref(legacy)
}
