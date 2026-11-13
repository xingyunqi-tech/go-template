// Package pagination builds the nestjs pagination and infinityPagination responses.
package pagination

import "math"

// Page is the standard list response {list, total, page, limit, totalPages}.
type Page[T any] struct {
	List       []T     `json:"list"`
	Total      float64 `json:"total"`
	Page       float64 `json:"page"`
	Limit      float64 `json:"limit"`
	TotalPages float64 `json:"totalPages"`
}

// Infinity is the infinite-scroll response {data, hasNextPage}.
type Infinity[T any] struct {
	Data        []T  `json:"data"`
	HasNextPage bool `json:"hasNextPage"`
}

// New returns a Page; a nil list is written as [].
func New[T any](list []T, total, page, limit float64) Page[T] {
	if list == nil {
		list = []T{}
	}

	return Page[T]{List: list, Total: total, Page: page, Limit: limit, TotalPages: math.Ceil(total / limit)}
}

// NewInfinity returns an Infinity page; hasNextPage is true when the page is full.
func NewInfinity[T any](data []T, limit int) Infinity[T] {
	if data == nil {
		data = []T{}
	}

	return Infinity[T]{Data: data, HasNextPage: len(data) == limit}
}

// OffsetLimit returns (page-1)*limit and limit rounded half away from zero, as PostgreSQL rounds them.
func OffsetLimit(page, limit float64) (offset, take int64) {
	return int64(math.Round((page - 1) * limit)), int64(math.Round(limit))
}

// LazyTotal is skip plus the rows found; ok is false when a count query is needed.
func LazyTotal(found int, page, limit float64) (total float64, ok bool) {
	skip := (page - 1) * limit
	if float64(found) == limit || (found == 0 && skip > 0) {
		return 0, false
	}

	return float64(found) + math.Max(skip, 0), true
}
