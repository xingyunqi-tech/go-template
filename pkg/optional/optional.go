// Package optional decodes optional JSON fields that may be null, telling an absent field from
// an explicit null.
package optional

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Field is an optional JSON field: Set is true when it was present, Value is nil when it was
// null.
type Field[T any] struct {
	Set   bool
	Value *T
}

// UnmarshalJSON implements json.Unmarshaler; it only runs for a present field.
func (f *Field[T]) UnmarshalJSON(data []byte) error {
	f.Set = true

	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		f.Value = nil

		return nil
	}

	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return fmt.Errorf("optional: %w", err)
	}

	f.Value = &v

	return nil
}

// MarshalJSON implements json.Marshaler.
func (f Field[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(f.Value) //nolint:wrapcheck // encoding errors of T
}
