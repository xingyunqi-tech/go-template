// Package region serves administrative divisions (nestjs RegionsService): lists, the tree and
// lookups by code are cached for 7 days, unknown codes for 60 seconds.
package region

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

const (
	_cachePrefix  = "region:"
	_cacheTTL     = 7 * 24 * time.Hour
	_missCacheTTL = time.Minute
	_searchLimit  = 20
	_province     = 1
)

// Region is a region as nestjs returns and caches it.
type Region struct {
	ID         int           `json:"id"`
	Code       string        `json:"code"`
	Name       string        `json:"name"`
	Level      int           `json:"level"`
	ParentCode *string       `json:"parentCode"`
	Sort       int           `json:"sort"`
	CreatedAt  jsontime.Time `json:"createdAt"`
	UpdatedAt  jsontime.Time `json:"updatedAt"`
}

// Node is a region with its children.
type Node struct {
	Region

	Children []*Node `json:"children"`
}

// Query selects regions: Keyword first, then ParentCode, then Level, else all regions.
type Query struct {
	Level      int
	ParentCode *string
	Keyword    string
}

// Service reads regions.
type Service struct {
	regions repo.Regions
	cache   repo.Cache
	log     *slog.Logger
}

// New returns a Service.
func New(regions repo.Regions, cache repo.Cache, log *slog.Logger) *Service {
	return &Service{regions: regions, cache: cache, log: log}
}

func fromEntity(e *entity.Region) Region {
	return Region{
		ID: e.ID, Code: e.Code, Name: e.Name, Level: e.Level, ParentCode: e.ParentCode, Sort: e.Sort,
		CreatedAt: jsontime.New(e.CreatedAt), UpdatedAt: jsontime.New(e.UpdatedAt),
	}
}

func fromEntities(es []entity.Region) []Region {
	out := make([]Region, len(es))
	for i := range es {
		out[i] = fromEntity(&es[i])
	}

	return out
}

// cachedList returns a cached non-empty list stored under key.
func cachedList[T any](ctx context.Context, cache repo.Cache, key string) (list []T, ok bool, err error) {
	found, err := cache.Get(ctx, _cachePrefix+key, &list)
	if err != nil {
		return nil, false, fmt.Errorf("region: cache: %w", err)
	}

	return list, found && len(list) > 0, nil
}

func storeList[T any](ctx context.Context, cache repo.Cache, key string, list []T) error {
	if len(list) == 0 {
		return nil
	}

	if err := cache.Set(ctx, _cachePrefix+key, list, _cacheTTL); err != nil {
		return fmt.Errorf("region: cache: %w", err)
	}

	return nil
}

func (s *Service) cachedRegions(ctx context.Context, key string, load func() ([]entity.Region, error)) ([]Region, error) {
	if list, ok, err := cachedList[Region](ctx, s.cache, key); err != nil || ok {
		return list, err
	}

	s.log.InfoContext(ctx, "Fetching regions from database", "key", key)

	es, err := load()
	if err != nil {
		return nil, err
	}

	list := fromEntities(es)

	return list, storeList(ctx, s.cache, key, list)
}

// Provinces returns the level 1 regions.
func (s *Service) Provinces(ctx context.Context) ([]Region, error) {
	return s.cachedRegions(ctx, "provinces", func() ([]entity.Region, error) {
		return s.regions.ListByLevel(ctx, _province)
	})
}

// Cities returns the children of a province.
func (s *Service) Cities(ctx context.Context, provinceCode string) ([]Region, error) {
	return s.cachedRegions(ctx, "cities:"+provinceCode, func() ([]entity.Region, error) {
		return s.regions.ListByParent(ctx, provinceCode)
	})
}

// Districts returns the children of a city.
func (s *Service) Districts(ctx context.Context, cityCode string) ([]Region, error) {
	return s.cachedRegions(ctx, "districts:"+cityCode, func() ([]entity.Region, error) {
		return s.regions.ListByParent(ctx, cityCode)
	})
}

// Query returns regions without caching.
func (s *Service) Query(ctx context.Context, q Query) ([]Region, error) {
	var (
		es  []entity.Region
		err error
	)

	switch {
	case q.Keyword != "":
		s.log.InfoContext(ctx, "Searching regions by keyword", "keyword", q.Keyword)
		es, err = s.regions.SearchByName(ctx, q.Keyword, _searchLimit)
	case q.ParentCode != nil:
		s.log.InfoContext(ctx, "Fetching regions by parent code", "parent_code", *q.ParentCode)
		es, err = s.regions.ListByParent(ctx, *q.ParentCode)
	case q.Level != 0:
		s.log.InfoContext(ctx, "Fetching regions by level", "level", q.Level)
		es, err = s.regions.ListByLevel(ctx, q.Level)
	default:
		s.log.InfoContext(ctx, "Fetching all regions")
		es, err = s.regions.ListAll(ctx)
	}

	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return fromEntities(es), nil
}

// Tree returns the provinces with their cities and districts.
func (s *Service) Tree(ctx context.Context) ([]*Node, error) {
	if tree, ok, err := cachedList[*Node](ctx, s.cache, "tree"); err != nil || ok {
		return tree, err
	}

	s.log.InfoContext(ctx, "Building region tree from database")

	es, err := s.regions.ListAll(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	nodes := make(map[string]*Node, len(es))
	for i := range es {
		nodes[es[i].Code] = &Node{Region: fromEntity(&es[i]), Children: []*Node{}}
	}

	tree := []*Node{}

	for i := range es {
		node := nodes[es[i].Code]

		switch parent := es[i].ParentCode; {
		case parent == nil:
			tree = append(tree, node)
		case nodes[*parent] != nil:
			nodes[*parent].Children = append(nodes[*parent].Children, node)
		}
	}

	return tree, storeList(ctx, s.cache, "tree", tree)
}

// cachedByCode looks code up in the cache; known is false when neither the region nor its absence
// is cached.
func (s *Service) cachedByCode(ctx context.Context, key, missKey string) (r *Region, known bool, err error) {
	if found, err := s.cache.Get(ctx, key, &r); err != nil {
		return nil, false, fmt.Errorf("region: cache: %w", err)
	} else if found && r != nil {
		return r, true, nil
	}

	var missing bool

	found, err := s.cache.Get(ctx, missKey, &missing)
	if err != nil {
		return nil, false, fmt.Errorf("region: cache: %w", err)
	}

	return nil, found && missing, nil
}

// ByCode returns the region, or nil when the code does not exist.
func (s *Service) ByCode(ctx context.Context, code string) (*Region, error) {
	key, missKey := _cachePrefix+"code:"+code, _cachePrefix+"code-miss:"+code

	if cached, known, err := s.cachedByCode(ctx, key, missKey); err != nil || known {
		return cached, err
	}

	s.log.InfoContext(ctx, "Fetching region by code from database", "code", code)

	e, err := s.regions.GetByCode(ctx, code)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if e == nil {
		if err := s.cache.Set(ctx, missKey, true, _missCacheTTL); err != nil {
			return nil, fmt.Errorf("region: cache: %w", err)
		}

		return nil, nil //nolint:nilnil // unknown code
	}

	r := fromEntity(e)

	if err := s.cache.Set(ctx, key, r, _cacheTTL); err != nil {
		return nil, fmt.Errorf("region: cache: %w", err)
	}

	return &r, nil
}
