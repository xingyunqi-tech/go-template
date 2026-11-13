package httperr

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_pgUniqueViolation  = "23505"
	_businessErrorField = "errorKey"
	_defaultTooMany     = "Too many requests, please try again later"
	_defaultInternal    = "Internal server error"
	_fallbackMessage    = "Error"
)

// Translate looks up an i18n key such as "common.notFound" in the request language.
type Translate func(key string) (string, bool)

// Body is a translated error response.
type Body struct {
	Status  int
	Message string
	// Data is nil (null) or the translated errors.
	Data    *validatorx.Object
	Headers http.Header
	// Cause names the error for logs.
	Cause string
}

type uniqueResponse struct {
	status int
	errors *validatorx.Object
	error  string
}

// uniqueConstraintResponses is nestjs UNIQUE_CONSTRAINT_RESPONSES (src/common/utils/exceptions.util.ts).
func uniqueConstraintResponses() map[string]uniqueResponse {
	return map[string]uniqueResponse{
		"UQ_e12875dfb3b1d92d7d7c5377e22":    {http.StatusUnprocessableEntity, Field("email", "emailAlreadyExists"), ""},
		"user_phone_key":                    {http.StatusUnprocessableEntity, Field("phone", "phoneAlreadyExists"), ""},
		"user_wechatOpenId_key":             {http.StatusUnprocessableEntity, Field("wechatOpenId", "wechatOpenIdAlreadyExists"), ""},
		"user_alipayOpenId_key":             {http.StatusUnprocessableEntity, Field("alipayOpenId", "alipayOpenIdAlreadyExists"), ""},
		"REL_75e2be4ce11d447ef43be0e374":    {http.StatusUnprocessableEntity, Field("photo", "alreadyExists"), ""},
		"UQ_favorite_user_target":           {http.StatusConflict, nil, "alreadyFavorited"},
		"UQ_question_subjectId_contentHash": {http.StatusConflict, nil, "duplicateQuestion"},
		"UQ_order_orderNo":                  {http.StatusInternalServerError, nil, ""},
		"UQ_share_shareCode":                {http.StatusInternalServerError, nil, ""},
	}
}

// statusKeys maps status codes to their common.* default message.
func statusKeys() map[int]string {
	return map[int]string{
		http.StatusBadRequest:          "badRequest",
		http.StatusUnauthorized:        "unauthorized",
		http.StatusForbidden:           "forbidden",
		http.StatusNotFound:            "notFound",
		http.StatusConflict:            "conflict",
		http.StatusGone:                "gone",
		http.StatusUnprocessableEntity: "unprocessableEntity",
		http.StatusTooManyRequests:     "tooManyRequests",
		http.StatusInternalServerError: "internalServerError",
	}
}

// UniqueViolation returns the constraint name of a PostgreSQL unique violation in err's chain.
func UniqueViolation(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == _pgUniqueViolation {
		return pgErr.ConstraintName, true
	}

	return "", false
}

// Build converts err to the response nestjs AllExceptionsFilter would send.
func Build(err error, translate Translate) Body {
	tr := translator{translate: translate}

	var (
		tooMany  *TooManyRequests
		business *entity.BusinessError
		invalid  *validatorx.ValidationError
		httpErr  *Error
	)

	switch {
	case errors.As(err, &tooMany):
		return Body{
			Status:  http.StatusTooManyRequests,
			Message: tr.common("tooManyRequests", _defaultTooMany),
			Data:    nil,
			Headers: tooMany.Headers,
			Cause:   "Too Many Requests",
		}
	case isUniqueViolation(err):
		return tr.unique(err)
	case errors.As(err, &business):
		return tr.business(business)
	case errors.As(err, &invalid):
		return tr.http(WithErrors(invalid.Status, invalid.Errors))
	case errors.As(err, &httpErr):
		return tr.http(httpErr)
	default:
		return tr.internal("Internal Server Error")
	}
}

func isUniqueViolation(err error) bool {
	_, ok := UniqueViolation(err)

	return ok
}

type translator struct {
	translate Translate
}

func (t translator) lookup(key string) (string, bool) {
	if t.translate == nil {
		return "", false
	}

	return t.translate("common." + key)
}

func (t translator) common(key, fallback string) string {
	if v, ok := t.lookup(key); ok {
		return v
	}

	return fallback
}

func (t translator) defaultMessage(status int) string {
	if key, ok := statusKeys()[status]; ok {
		if v, found := t.lookup(key); found {
			return v
		}
	}

	if name, ok := nestHTTPStatus()[status]; ok {
		return name
	}

	return _fallbackMessage
}

func (t translator) internal(cause string) Body {
	return Body{
		Status:  http.StatusInternalServerError,
		Message: t.common("internalServerError", _defaultInternal),
		Data:    nil,
		Headers: nil,
		Cause:   cause,
	}
}

func (t translator) translateErrors(errs *validatorx.Object) *validatorx.Object {
	if errs == nil {
		return nil
	}

	out := validatorx.NewObject()

	for _, k := range errs.Keys() {
		v, _ := errs.Get(k)
		if s, ok := v.(string); ok {
			v = t.common(s, s)
		}

		out.Set(k, v)
	}

	return out
}

func (t translator) unique(err error) Body {
	constraint, _ := UniqueViolation(err)

	mapped, ok := uniqueConstraintResponses()[constraint]
	if !ok {
		mapped = uniqueResponse{status: http.StatusConflict, errors: nil, error: ""}
	}

	message := t.defaultMessage(mapped.status)
	if mapped.error != "" {
		message = t.common(mapped.error, mapped.error)
	}

	cause := constraint
	if cause == "" {
		cause = "unknown"
	}

	return Body{
		Status:  mapped.status,
		Message: message,
		Data:    t.translateErrors(mapped.errors),
		Headers: nil,
		Cause:   "Unique constraint violation " + cause,
	}
}

func (t translator) business(e *entity.BusinessError) Body {
	status, err := businessStatus(e.Key)
	if err != nil {
		return t.internal(err.Error())
	}

	data := validatorx.NewObject()
	for _, d := range e.Details {
		data.Set(d.Name, d.Value)
	}

	data.Set(_businessErrorField, e.Key)

	return Body{
		Status:  status,
		Message: t.common(e.Key, e.Key),
		Data:    data,
		Headers: nil,
		Cause:   e.Key,
	}
}

func (t translator) http(e *Error) Body {
	var message string

	switch {
	case e.Message != "":
		message = t.common(e.Message, e.Message)
	case e.ErrorKey != "":
		message = t.common(e.ErrorKey, e.ErrorKey)
	default:
		message = t.defaultMessage(e.Status)
	}

	return Body{
		Status:  e.Status,
		Message: message,
		Data:    t.translateErrors(e.Errors),
		Headers: e.Headers,
		Cause:   e.Error(),
	}
}

// nestHTTPStatus is the @nestjs/common HttpStatus enum, used verbatim for statuses without an
// i18n key.
func nestHTTPStatus() map[int]string {
	return map[int]string{
		100: "CONTINUE", 101: "SWITCHING_PROTOCOLS", 102: "PROCESSING", 103: "EARLYHINTS",
		200: "OK", 201: "CREATED", 202: "ACCEPTED", 203: "NON_AUTHORITATIVE_INFORMATION",
		204: "NO_CONTENT", 205: "RESET_CONTENT", 206: "PARTIAL_CONTENT", 207: "MULTI_STATUS",
		208: "ALREADY_REPORTED", 210: "CONTENT_DIFFERENT",
		300: "AMBIGUOUS", 301: "MOVED_PERMANENTLY", 302: "FOUND", 303: "SEE_OTHER",
		304: "NOT_MODIFIED", 307: "TEMPORARY_REDIRECT", 308: "PERMANENT_REDIRECT",
		400: "BAD_REQUEST", 401: "UNAUTHORIZED", 402: "PAYMENT_REQUIRED", 403: "FORBIDDEN",
		404: "NOT_FOUND", 405: "METHOD_NOT_ALLOWED", 406: "NOT_ACCEPTABLE",
		407: "PROXY_AUTHENTICATION_REQUIRED", 408: "REQUEST_TIMEOUT", 409: "CONFLICT", 410: "GONE",
		411: "LENGTH_REQUIRED", 412: "PRECONDITION_FAILED", 413: "PAYLOAD_TOO_LARGE",
		414: "URI_TOO_LONG", 415: "UNSUPPORTED_MEDIA_TYPE", 416: "REQUESTED_RANGE_NOT_SATISFIABLE",
		417: "EXPECTATION_FAILED", 418: "I_AM_A_TEAPOT", 421: "MISDIRECTED",
		422: "UNPROCESSABLE_ENTITY", 423: "LOCKED", 424: "FAILED_DEPENDENCY",
		428: "PRECONDITION_REQUIRED", 429: "TOO_MANY_REQUESTS", 456: "UNRECOVERABLE_ERROR",
		500: "INTERNAL_SERVER_ERROR", 501: "NOT_IMPLEMENTED", 502: "BAD_GATEWAY",
		503: "SERVICE_UNAVAILABLE", 504: "GATEWAY_TIMEOUT", 505: "HTTP_VERSION_NOT_SUPPORTED",
		507: "INSUFFICIENT_STORAGE", 508: "LOOP_DETECTED",
	}
}
