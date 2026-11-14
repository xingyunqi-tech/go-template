package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Regions implements repo.Regions.
type Regions struct {
	db *DB
}

// NewRegions returns the region repository.
func NewRegions(db *DB) *Regions {
	return &Regions{db: db}
}

var _ repo.Regions = (*Regions)(nil)

func toRegion(r *sqlcgen.Region) entity.Region {
	return entity.Region{
		ID: int(r.ID), Code: r.Code, Name: r.Name, Level: int(r.Level), ParentCode: r.ParentCode,
		Sort: int(r.Sort), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func (r *Regions) list(ctx context.Context, op string, fn func(*sqlcgen.Queries) ([]*sqlcgen.Region, error)) ([]entity.Region, error) {
	out := []entity.Region{}

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := fn(q)
		for _, row := range rows {
			out = append(out, toRegion(row))
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("regions: %s: %w", op, err)
	}

	return out, nil
}

// GetByCode returns the region, or nil.
func (r *Regions) GetByCode(ctx context.Context, code string) (*entity.Region, error) {
	var out *entity.Region

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.GetRegionByCode(ctx, code)
		if notFound(err) {
			return nil
		}

		if err == nil {
			region := toRegion(row)
			out = &region
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("regions: get: %w", err)
	}

	return out, nil
}

// ListByLevel returns the regions of a level.
func (r *Regions) ListByLevel(ctx context.Context, level int) ([]entity.Region, error) {
	return r.list(ctx, "by level", func(q *sqlcgen.Queries) ([]*sqlcgen.Region, error) {
		return q.ListRegionsByLevel(ctx, id32(level))
	})
}

// ListByParent returns the children of a region.
func (r *Regions) ListByParent(ctx context.Context, parentCode string) ([]entity.Region, error) {
	return r.list(ctx, "by parent", func(q *sqlcgen.Queries) ([]*sqlcgen.Region, error) {
		return q.ListRegionsByParent(ctx, &parentCode)
	})
}

// ListAll returns every region.
func (r *Regions) ListAll(ctx context.Context) ([]entity.Region, error) {
	return r.list(ctx, "all", func(q *sqlcgen.Queries) ([]*sqlcgen.Region, error) {
		return q.ListRegions(ctx)
	})
}

var _likeEscaper = strings.NewReplacer("%", `\%`, "_", `\_`) //nolint:gochecknoglobals // immutable replacer

// SearchByName escapes % and _ in keyword; a backslash keeps its LIKE meaning.
func (r *Regions) SearchByName(ctx context.Context, keyword string, limit int) ([]entity.Region, error) {
	pattern := "%" + _likeEscaper.Replace(keyword) + "%"

	return r.list(ctx, "search", func(q *sqlcgen.Queries) ([]*sqlcgen.Region, error) {
		return q.SearchRegions(ctx, sqlcgen.SearchRegionsParams{Pattern: pattern, RowLimit: id32(limit)})
	})
}
