// Package httperr models nestjs HttpException and turns any error into the {code, message, data}
// error body following nestjs AllExceptionsFilter.
package httperr

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// Error is a nestjs HttpException. Message is a custom message (translated as common.<Message>
// when the key exists); ErrorKey is the response "error" field used when Message is empty;
// Errors becomes data, with top-level string values translated.
type Error struct {
	Status   int
	Message  string
	ErrorKey string
	Errors   *validatorx.Object
	Headers  http.Header
}

func (e *Error) Error() string {
	switch {
	case e.Message != "":
		return strconv.Itoa(e.Status) + " " + e.Message
	case e.ErrorKey != "":
		return strconv.Itoa(e.Status) + " " + e.ErrorKey
	default:
		return strconv.Itoa(e.Status) + " " + http.StatusText(e.Status)
	}
}

// New returns an exception with a custom message, like `new NotFoundException('…')`.
func New(status int, message string) *Error {
	return &Error{Status: status, Message: message, ErrorKey: "", Errors: nil, Headers: nil}
}

// WithErrors returns an exception whose data is errors, like `{status, errors}` bodies.
func WithErrors(status int, errs *validatorx.Object) *Error {
	return &Error{Status: status, Message: "", ErrorKey: "", Errors: errs, Headers: nil}
}

// Keyed returns an exception whose message is common.<key>, like nestjs throwNotFoundError(key).
func Keyed(status int, key string) *Error {
	return &Error{Status: status, Message: "", ErrorKey: key, Errors: nil, Headers: nil}
}

// Field returns errors with a single field, like nestjs throwValidationError(field, key).
func Field(name string, value validatorx.Value) *validatorx.Object {
	o := validatorx.NewObject()
	o.Set(name, value)

	return o
}

// InvalidID is the ParseIdPipe failure: 400 {id: invalidId}.
func InvalidID() *Error {
	return WithErrors(http.StatusBadRequest, Field("id", "invalidId"))
}

// TooManyRequests is the throttler rejection; headers carry Retry-After.
type TooManyRequests struct {
	Headers http.Header
}

func (e *TooManyRequests) Error() string {
	return "ThrottlerException: Too Many Requests"
}

// ErrUnknownBusinessError reports a BusinessError whose key is not in the key table.
var ErrUnknownBusinessError = errors.New("httperr: unregistered business error key")

func businessStatus(key string) (int, error) {
	status, ok := businessErrors()[key]
	if !ok {
		return 0, fmt.Errorf("%w: %s", ErrUnknownBusinessError, key)
	}

	return status, nil
}

// businessErrors is nestjs src/common/constants/business-error-keys.ts.
func businessErrors() map[string]int {
	return map[string]int{
		"sessionTakenOver":          http.StatusConflict,
		"sessionClosed":             http.StatusConflict,
		"questionInMockInProgress":  http.StatusConflict,
		"answerHidden":              http.StatusForbidden,
		"selfAssessmentNotAllowed":  http.StatusConflict,
		"questionNotAccessible":     http.StatusForbidden,
		"noQuestionsAvailable":      http.StatusUnprocessableEntity,
		"generateLimitExceeded":     http.StatusTooManyRequests,
		"mockSessionBusy":           http.StatusConflict,
		"paperRequired":             http.StatusUnprocessableEntity,
		"reportNotAvailable":        http.StatusConflict,
		"dailySessionCannotFinish":  http.StatusConflict,
		"dailySessionCannotAbandon": http.StatusConflict,
		"aiUnavailable":             http.StatusServiceUnavailable,
		"aiQuotaExceeded":           http.StatusTooManyRequests,
		"aiRateLimited":             http.StatusTooManyRequests,
		"aiConsentRequired":         http.StatusForbidden,
		"phoneBindingRequired":      http.StatusForbidden,
		"contentBlocked":            http.StatusUnprocessableEntity,
		"aiConversationOutdated":    http.StatusConflict,
		"errataInProgress":          http.StatusConflict,
		"answerChangeNotAllowed":    http.StatusUnprocessableEntity,
	}
}

// Status returns an exception with the default message of status.
func Status(status int) *Error {
	return &Error{Status: status, Message: "", ErrorKey: "", Errors: nil, Headers: nil}
}
