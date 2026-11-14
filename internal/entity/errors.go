// Package entity holds domain objects and domain errors.
package entity

// Detail is one extra field of a BusinessError, written into the error response data.
type Detail struct {
	Name  string
	Value any
}

// BusinessError is nestjs BusinessException: Key selects the status code and the common.<Key>
// message; Details are returned untranslated in data next to errorKey.
type BusinessError struct {
	Key     string
	Details []Detail
}

// NewBusinessError returns a BusinessError.
func NewBusinessError(key string, details ...Detail) *BusinessError {
	return &BusinessError{Key: key, Details: details}
}

func (e *BusinessError) Error() string {
	return "business error: " + e.Key
}

// Error is a nestjs HttpException raised by business code: Message is a custom message, ErrorKey
// the "error" field and Fields the "errors" object.
type Error struct {
	Status   int
	Message  string
	ErrorKey string
	Fields   []Detail
}

func (e *Error) Error() string {
	switch {
	case e.Message != "":
		return e.Message
	case e.ErrorKey != "":
		return e.ErrorKey
	case len(e.Fields) > 0:
		return e.Fields[0].Name + ": " + toString(e.Fields[0].Value)
	default:
		return "http error"
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return "value"
}

// HTTP statuses used by business errors.
const (
	StatusBadRequest    = 400
	StatusUnauthorized  = 401
	StatusForbidden     = 403
	StatusNotFound      = 404
	StatusUnprocessable = 422
)

// Invalid is nestjs throwValidationError(field, key): 422 {field: key}.
func Invalid(field, key string) *Error {
	return &Error{Status: StatusUnprocessable, Message: "", ErrorKey: "", Fields: []Detail{{Name: field, Value: key}}}
}

// InvalidFields is a 422 with several fields.
func InvalidFields(fields ...Detail) *Error {
	return &Error{Status: StatusUnprocessable, Message: "", ErrorKey: "", Fields: fields}
}

// Unauthorized is a 401 with an optional untranslated message.
func Unauthorized(message string) *Error {
	return &Error{Status: StatusUnauthorized, Message: message, ErrorKey: "", Fields: nil}
}

// Forbidden is a 403 with a custom message.
func Forbidden(message string) *Error {
	return &Error{Status: StatusForbidden, Message: message, ErrorKey: "", Fields: nil}
}

// NotFoundKey is nestjs throwNotFoundError(key).
func NotFoundKey(key string) *Error {
	return &Error{Status: StatusNotFound, Message: "", ErrorKey: key, Fields: nil}
}

// NotFound is a 404 with a custom message.
func NotFound(message string) *Error {
	return &Error{Status: StatusNotFound, Message: message, ErrorKey: "", Fields: nil}
}
