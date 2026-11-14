// Package appconfig is the remote config (nestjs AppConfigService): cache keys carry a write
// version, so a write makes readers load the new version.
package appconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_cacheTTL       = time.Minute
	_versionTTL     = 24 * time.Hour
	_initialVersion = "0"
	_valueField     = "value"
)

// Config is an entry as nestjs returns and caches it; registered entries that were never
// written have null times.
type Config struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	IsPublic  bool            `json:"isPublic"`
	CreatedAt *jsontime.Time  `json:"createdAt"`
	UpdatedAt *jsontime.Time  `json:"updatedAt"`
}

type cachedConfig struct {
	Config *Config `json:"config"`
}

// VersionKey is the cache key holding the write version of key.
func VersionKey(key string) string {
	return "config:version:" + key
}

// CacheKey is the cache key of key at a write version.
func CacheKey(key, version string) string {
	return "config:" + key + ":" + version
}

// Service reads and writes config entries.
type Service struct {
	configs repo.Configs
	cache   repo.Cache
	tx      repo.Transactions
	log     *slog.Logger
}

// New returns a Service.
func New(configs repo.Configs, cache repo.Cache, tx repo.Transactions, log *slog.Logger) *Service {
	return &Service{configs: configs, cache: cache, tx: tx, log: log}
}

func notFound() error {
	return entity.NotFoundKey("configNotFound")
}

// FindPublic returns a public entry.
func (s *Service) FindPublic(ctx context.Context, key string) (*Config, error) {
	c, err := s.effective(ctx, key)
	if err != nil {
		return nil, err
	}

	if c == nil || !c.IsPublic {
		return nil, notFound()
	}

	return c, nil
}

// Find returns an entry, public or not.
func (s *Service) Find(ctx context.Context, key string) (*Config, error) {
	c, err := s.effective(ctx, key)
	if err != nil {
		return nil, err
	}

	if c == nil {
		return nil, notFound()
	}

	return c, nil
}

// Value returns value.value of a registered entry, or its default; ok is false when neither
// exists.
func (s *Service) Value(ctx context.Context, key string) (value json.RawMessage, ok bool, err error) {
	c, err := s.load(ctx, key, true)
	if err != nil {
		return nil, false, err
	}

	if c != nil {
		var fields map[string]json.RawMessage
		if json.Unmarshal(c.Value, &fields) == nil {
			if v, has := fields[_valueField]; has {
				return v, true, nil
			}
		}
	}

	if d, registered := Find(key); registered && d.Default != nil {
		raw, err := json.Marshal(d.Default)
		if err != nil {
			return nil, false, fmt.Errorf("appconfig: default: %w", err)
		}

		return raw, true, nil
	}

	return nil, false, nil
}

// UpsertGeneric is the generic write endpoint; it rejects dedicated entries.
func (s *Service) UpsertGeneric(ctx context.Context, key string, value json.RawMessage, isPublic *bool) (*Config, error) {
	if d, ok := Find(key); ok && d.Dedicated {
		return nil, entity.Invalid("key", "configManagedByDedicatedEndpoint")
	}

	return s.Upsert(ctx, key, value, isPublic)
}

// Upsert writes an entry; registered entries take isPublic from their definition. The returned
// value is the written one, in its original key order.
func (s *Service) Upsert(ctx context.Context, key string, value json.RawMessage, isPublic *bool) (*Config, error) {
	s.log.InfoContext(ctx, "Upsert config", "key", key)

	d, registered := Find(key)
	if registered {
		isPublic = &d.IsPublic
	}

	stored, err := s.configs.Upsert(ctx, key, value, isPublic)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	s.tx.AfterCommit(ctx, func(ctx context.Context) {
		if err := s.cache.Set(ctx, VersionKey(key), uuid.New().String(), _versionTTL); err != nil {
			s.log.WarnContext(ctx, "config version not bumped", "key", key, "error", err.Error())
		}
	})

	c := fromEntity(stored)
	c.Value = value

	return c, nil
}

func fromEntity(e *entity.Config) *Config {
	created, updated := jsontime.New(e.CreatedAt), jsontime.New(e.UpdatedAt)

	return &Config{Key: e.Key, Value: jsJSON(e.Value), IsPublic: e.IsPublic, CreatedAt: &created, UpdatedAt: &updated}
}

// jsJSON rewrites stored JSON as JSON.stringify(JSON.parse(raw)) does, so numbers such as 1.0 read
// back as 1.
func jsJSON(raw []byte) json.RawMessage {
	v, err := validatorx.ParseJSON(raw)
	if err != nil {
		return raw
	}

	if s, ok := validatorx.Stringify(v); ok {
		return json.RawMessage(s)
	}

	return raw
}

func (s *Service) effective(ctx context.Context, key string) (*Config, error) {
	d, registered := Find(key)

	stored, err := s.load(ctx, key, registered)
	if err != nil {
		return nil, err
	}

	switch {
	case stored != nil:
		if registered {
			stored.IsPublic = d.IsPublic
		}

		return stored, nil
	case !registered || d.Default == nil:
		return nil, nil //nolint:nilnil // absent entry
	}

	value, err := json.Marshal(map[string]any{_valueField: d.Default})
	if err != nil {
		return nil, fmt.Errorf("appconfig: default: %w", err)
	}

	return &Config{Key: key, Value: value, IsPublic: d.IsPublic, CreatedAt: nil, UpdatedAt: nil}, nil
}

// load reads the stored entry through the cache; cacheMissing also caches its absence.
func (s *Service) load(ctx context.Context, key string, cacheMissing bool) (*Config, error) {
	version := _initialVersion
	if _, err := s.cache.Get(ctx, VersionKey(key), &version); err != nil {
		return nil, fmt.Errorf("appconfig: cache: %w", err)
	}

	cacheKey := CacheKey(key, version)

	var cached cachedConfig

	found, err := s.cache.Get(ctx, cacheKey, &cached)
	if err != nil {
		return nil, fmt.Errorf("appconfig: cache: %w", err)
	}

	if found {
		return cached.Config, nil
	}

	e, err := s.configs.Get(ctx, key)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	var c *Config
	if e != nil {
		c = fromEntity(e)
	}

	if c != nil || cacheMissing {
		if err := s.cache.Set(ctx, cacheKey, cachedConfig{Config: c}, _cacheTTL); err != nil {
			return nil, fmt.Errorf("appconfig: cache: %w", err)
		}
	}

	return c, nil
}
