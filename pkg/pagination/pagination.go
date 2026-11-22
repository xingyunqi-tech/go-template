// Package pagination holds the page-number and cursor list shapes of platform conventions §3,
// the opaque cursor and the sort parameter.
package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// Limits shared by page sizes and cursor limits.
const (
	DefaultSize = 20
	MaxSize     = 100
)

var (
	// ErrInvalidCursor reports a cursor that this service did not issue.
	ErrInvalidCursor = errors.New("pagination: invalid cursor")
	// ErrInvalidSort reports a sort field outside the whitelist.
	ErrInvalidSort = errors.New("pagination: invalid sort")
)

// Page is the page-number list response.
type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

// NewPage returns a Page; nil items are written as [].
func NewPage[T any](items []T, total int64, page, pageSize int) Page[T] {
	if items == nil {
		items = []T{}
	}

	return Page[T]{Items: items, Total: total, Page: page, PageSize: pageSize}
}

// Offset is the number of rows before page.
func Offset(page, pageSize int) int {
	return (page - 1) * pageSize
}

// CursorPage is the cursor list response.
type CursorPage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

// Cursor is the position after the last item of a page: its sort key and id.
type Cursor struct {
	Key string `json:"k"`
	ID  string `json:"i"`
}

// Encode returns the opaque base64url form.
func (c Cursor) Encode() string {
	raw, _ := json.Marshal(c) //nolint:errchkjson // two strings

	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor reads a cursor returned by Encode.
func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, ErrInvalidCursor
	}

	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil || c.ID == "" {
		return Cursor{}, ErrInvalidCursor
	}

	return c, nil
}

// NewCursorPage returns the page of items fetched with limit+1 rows: the extra row only tells
// whether a next page exists, and cursorOf gives the cursor of the last returned item.
func NewCursorPage[T any](rows []T, limit int, cursorOf func(T) Cursor) CursorPage[T] {
	page := CursorPage[T]{Items: rows, NextCursor: nil}
	if page.Items == nil {
		page.Items = []T{}
	}

	if len(rows) > limit {
		page.Items = rows[:limit]
		next := cursorOf(page.Items[limit-1]).Encode()
		page.NextCursor = &next
	}

	return page
}

// Sort is one field of sort=field,-field2.
type Sort struct {
	Field      string
	Descending bool
}

// ParseSort reads a sort parameter; an empty one is def, and every field must be in allowed.
func ParseSort(s, def string, allowed []string) ([]Sort, error) {
	if strings.TrimSpace(s) == "" {
		s = def
	}

	var out []Sort

	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		field, desc := strings.CutPrefix(part, "-")

		if !slices.Contains(allowed, field) {
			return nil, ErrInvalidSort
		}

		out = append(out, Sort{Field: field, Descending: desc})
	}

	return out, nil
}
