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
