package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Configs implements repo.Configs.
type Configs struct {
	db *DB
}

// NewConfigs returns the config repository.
func NewConfigs(db *DB) *Configs {
	return &Configs{db: db}
}

var _ repo.Configs = (*Configs)(nil)

func toConfig(c *sqlcgen.Config) *entity.Config {
	return &entity.Config{Key: c.Key, Value: c.Value, IsPublic: c.IsPublic, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// Get returns the entry, or nil.
func (r *Configs) Get(ctx context.Context, key string) (*entity.Config, error) {
	var out *entity.Config

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		c, err := q.GetConfig(ctx, key)
		if notFound(err) {
			return nil
		}

		if err == nil {
			out = toConfig(c)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("config: get: %w", err)
	}

	return out, nil
}

// Upsert writes the entry and returns it as stored.
func (r *Configs) Upsert(ctx context.Context, key string, value []byte, isPublic *bool) (*entity.Config, error) {
	var out *entity.Config

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		c, err := q.UpsertConfig(ctx, sqlcgen.UpsertConfigParams{Key: key, Value: value, IsPublic: isPublic})
		if notFound(err) {
			c, err = q.GetConfig(ctx, key)
		}

		if err == nil {
			out = toConfig(c)
		}

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("config: upsert: %w", err)
	}

	return out, nil
}
