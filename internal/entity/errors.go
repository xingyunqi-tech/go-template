// Package entity holds domain objects and domain errors.
package entity

import (
	"errors"
	"strings"
)

// Error keys of the error code table (platform conventions §2.4).
const (
	KeyBadRequest               = "common.badRequest"
	KeyMalformedJSON            = "common.malformedJson"
	KeyUnauthorized             = "common.unauthorized"
	KeyTokenExpired             = "auth.tokenExpired"
	KeyInvalidCredentials       = "auth.invalidCredentials"
	KeyRefreshTokenReused       = "auth.refreshTokenReused"
	KeyRefreshTokenInvalid      = "auth.refreshTokenInvalid"
	KeyForbidden                = "common.forbidden"
	KeyAccountDisabled          = "auth.accountDisabled"
	KeyEmailNotVerified         = "auth.emailNotVerified"
	KeyReauthRequired           = "auth.reauthRequired"
	KeyNotFound                 = "common.notFound"
	KeyProviderDisabled         = "auth.providerDisabled"
	KeySessionNotFound          = "auth.sessionNotFound"
	KeyUserNotFound             = "user.notFound"
	KeyMethodNotAllowed         = "common.methodNotAllowed"
	KeyConflict                 = "common.conflict"
	KeyEmailTaken               = "user.emailTaken"
	KeyPhoneTaken               = "user.phoneTaken"
	KeyIdempotencyInProgress    = "common.idempotencyInProgress"
	KeyLastAdmin                = "role.lastAdmin"
	KeyPayloadTooLarge          = "common.payloadTooLarge"
	KeyFileTooLarge             = "file.tooLarge"
	KeyUnsupportedMediaType     = "common.unsupportedMediaType"
	KeyValidationFailed         = "common.validationFailed"
	KeyVerificationCodeInvalid  = "auth.verificationCodeInvalid"
	KeyVerificationTokenInvalid = "auth.verificationTokenInvalid"
	KeyPasswordIncorrect        = "auth.passwordIncorrect"
	KeyFileTypeNotAllowed       = "file.typeNotAllowed"
	KeyIdempotencyKeyMismatch   = "common.idempotencyKeyMismatch"
	KeyProviderCodeInvalid      = "auth.providerCodeInvalid"
	KeyTooManyRequests          = "common.tooManyRequests"
	KeySendTooFrequent          = "auth.sendTooFrequent"
	KeyVerificationLocked       = "auth.verificationLocked"
	KeyInternalError            = "common.internalError"
	KeyBadGateway               = "common.badGateway"
	KeyServiceUnavailable       = "common.serviceUnavailable"
)

// Code is the HTTP status and business code of an error key.
type Code struct {
	Status int
	Code   int
}

// Codes returns the error code table keyed by error key.
func Codes() map[string]Code {
	return map[string]Code{
		KeyBadRequest:               {400, 40000},
		KeyMalformedJSON:            {400, 40001},
		KeyUnauthorized:             {401, 40100},
		KeyTokenExpired:             {401, 40101},
		KeyInvalidCredentials:       {401, 40102},
		KeyRefreshTokenReused:       {401, 40103},
		KeyRefreshTokenInvalid:      {401, 40104},
		KeyForbidden:                {403, 40300},
		KeyAccountDisabled:          {403, 40301},
		KeyEmailNotVerified:         {403, 40302},
		KeyReauthRequired:           {403, 40303},
		KeyNotFound:                 {404, 40400},
		KeyProviderDisabled:         {404, 40401},
		KeySessionNotFound:          {404, 40402},
		KeyUserNotFound:             {404, 40403},
		KeyMethodNotAllowed:         {405, 40500},
		KeyConflict:                 {409, 40900},
		KeyEmailTaken:               {409, 40901},
		KeyPhoneTaken:               {409, 40902},
		KeyIdempotencyInProgress:    {409, 40903},
		KeyLastAdmin:                {409, 40904},
		KeyPayloadTooLarge:          {413, 41300},
		KeyFileTooLarge:             {413, 41301},
		KeyUnsupportedMediaType:     {415, 41500},
		KeyValidationFailed:         {422, 42200},
		KeyVerificationCodeInvalid:  {422, 42201},
		KeyVerificationTokenInvalid: {422, 42202},
		KeyPasswordIncorrect:        {422, 42203},
		KeyFileTypeNotAllowed:       {422, 42204},
		KeyIdempotencyKeyMismatch:   {422, 42205},
		KeyProviderCodeInvalid:      {422, 42206},
		KeyTooManyRequests:          {429, 42900},
		KeySendTooFrequent:          {429, 42901},
		KeyVerificationLocked:       {429, 42902},
		KeyInternalError:            {500, 50000},
		KeyBadGateway:               {502, 50200},
		KeyServiceUnavailable:       {503, 50300},
	}
}

// Error is a failure identified by an error key; Data is returned as the response data and
// Cause is kept for logs only.
type Error struct {
	Key   string
	Data  any
	Cause error
}

// New returns an Error for key.
func New(key string) *Error {
	return &Error{Key: key, Data: nil, Cause: nil}
}

// Wrap returns an Error for key caused by err.
func Wrap(key string, err error) *Error {
	return &Error{Key: key, Data: nil, Cause: err}
}

// WithData returns a copy of e carrying data.
func (e *Error) WithData(data any) *Error {
	return &Error{Key: e.Key, Data: data, Cause: e.Cause}
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return e.Key + ": " + e.Cause.Error()
	}

	return e.Key
}

func (e *Error) Unwrap() error {
	return e.Cause
}

// Is matches another *Error with the same key.
func (e *Error) Is(target error) bool {
	var t *Error

	return errors.As(target, &t) && t.Key == e.Key
}

// Validation rules (platform conventions §2.3).
const (
	RuleRequired  = "required"
	RuleType      = "type"
	RuleEmail     = "email"
	RuleURL       = "url"
	RuleUUID      = "uuid"
	RuleMin       = "min"
	RuleMax       = "max"
	RuleMinLength = "minLength"
	RuleMaxLength = "maxLength"
	RuleMinItems  = "minItems"
	RuleMaxItems  = "maxItems"
	RulePattern   = "pattern"
	RuleEnum      = "enum"
	RuleFormat    = "format"
	RuleExists    = "exists"
	RuleContains  = "contains"
)

// RuleMinProperties is the service rule of an object with fewer properties than allowed.
const RuleMinProperties = "minProperties"

// FieldError is one failed constraint: Field is a JSON path, query.<name> or path.<name>.
type FieldError struct {
	Field  string
	Rule   string
	Params map[string]any
}

// ValidationError is a 422 common.validationFailed with one entry per failed field.
type ValidationError struct {
	Errors []FieldError
}

// Invalid returns a ValidationError for one field.
func Invalid(field, rule string, params map[string]any) *ValidationError {
	return &ValidationError{Errors: []FieldError{{Field: field, Rule: rule, Params: params}}}
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Errors))
	for _, f := range e.Errors {
		parts = append(parts, f.Field+": "+f.Rule)
	}

	return KeyValidationFailed + ": " + strings.Join(parts, ", ")
}
