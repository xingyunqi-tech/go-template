package region_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/region"
)

var _stamp = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixture

type fakeRegions struct {
	all   []entity.Region
	calls []string
}

func (f *fakeRegions) GetByCode(_ context.Context, code string) (*entity.Region, error) {
	f.calls = append(f.calls, "code:"+code)

	for i := range f.all {
		if f.all[i].Code == code {
			return &f.all[i], nil
		}
	}

	return nil, nil //nolint:nilnil // unknown code
}

func (f *fakeRegions) ListByLevel(_ context.Context, level int) ([]entity.Region, error) {
	f.calls = append(f.calls, "level")

	var out []entity.Region

	for _, r := range f.all {
		if r.Level == level {
			out = append(out, r)
		}
	}

	return out, nil
}

func (f *fakeRegions) ListByParent(_ context.Context, parentCode string) ([]entity.Region, error) {
	f.calls = append(f.calls, "parent:"+parentCode)

	var out []entity.Region

	for _, r := range f.all {
		if r.ParentCode != nil && *r.ParentCode == parentCode {
			out = append(out, r)
		}
	}

	return out, nil
}

func (f *fakeRegions) ListAll(context.Context) ([]entity.Region, error) {
	f.calls = append(f.calls, "all")

	return f.all, nil
}

func (f *fakeRegions) SearchByName(_ context.Context, keyword string, limit int) ([]entity.Region, error) {
	f.calls = append(f.calls, "search:"+keyword)

	return f.all[:min(limit, 1)], nil
}

func newRegion(id int, code string, level int, parent string) entity.Region {
	r := entity.Region{ID: id, Code: code, Name: code, Level: level, ParentCode: nil, Sort: id, CreatedAt: _stamp, UpdatedAt: _stamp}
	if parent != "" {
		r.ParentCode = &parent
	}

	return r
}

func newService() (*region.Service, *fakeRegions, *redis.Memory) {
	repo := &fakeRegions{all: []entity.Region{
		newRegion(1, "110000000000", 1, ""),
		newRegion(2, "120000000000", 1, ""),
		newRegion(3, "110100000000", 2, "110000000000"),
		newRegion(4, "110101000000", 3, "110100000000"),
		newRegion(5, "990100000000", 2, "990000000000"),
	}, calls: nil}
	cache := redis.NewMemory()

	return region.New(repo, cache, slog.New(slog.DiscardHandler)), repo, cache
}

func TestTreeNestsChildrenAndDropsOrphans(t *testing.T) {
	t.Parallel()

	svc, repo, cache := newService()

	tree, err := svc.Tree(t.Context())
	require.NoError(t, err)

	raw, err := json.Marshal(tree)
	require.NoError(t, err)

	node := func(id int, code string, level int, parent, children string) string {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}

		return `{"id":` + strconv.Itoa(id) + `,"code":"` + code + `","name":"` + code + `","level":` + strconv.Itoa(level) +
			`,"parentCode":` + p + `,"sort":` + strconv.Itoa(id) +
			`,"createdAt":"2026-11-10T12:00:00.000Z","updatedAt":"2026-11-10T12:00:00.000Z","children":[` + children + `]}`
	}
	want := `[` + node(1, "110000000000", 1, "", node(3, "110100000000", 2, "110000000000", node(4, "110101000000", 3, "110100000000", ""))) +
		`,` + node(2, "120000000000", 1, "", "") + `]`
	assert.Equal(t, want, string(raw))

	var cached json.RawMessage

	found, err := cache.Get(t.Context(), "region:tree", &cached)
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, want, string(cached))

	_, err = svc.Tree(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{"all"}, repo.calls)
}

func TestListsAreCachedWhenNotEmpty(t *testing.T) {
	t.Parallel()

	svc, repo, cache := newService()
	ctx := t.Context()

	for range 2 {
		provinces, err := svc.Provinces(ctx)
		require.NoError(t, err)
		assert.Len(t, provinces, 2)

		cities, err := svc.Cities(ctx, "110000000000")
		require.NoError(t, err)
		assert.Len(t, cities, 1)

		none, err := svc.Districts(ctx, "120100000000")
		require.NoError(t, err)
		assert.Equal(t, []region.Region{}, none)
	}

	assert.Equal(t, []string{"level", "parent:110000000000", "parent:120100000000", "parent:120100000000"}, repo.calls)

	var cached []region.Region

	found, err := cache.Get(ctx, "region:cities:110000000000", &cached)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "110100000000", cached[0].Code)
}

func TestByCodeCachesHitsAndMisses(t *testing.T) {
	t.Parallel()

	svc, repo, cache := newService()
	ctx := t.Context()

	for range 2 {
		r, err := svc.ByCode(ctx, "110101000000")
		require.NoError(t, err)
		assert.Equal(t, "110100000000", *r.ParentCode)

		missing, err := svc.ByCode(ctx, "999999999998")
		require.NoError(t, err)
		assert.Nil(t, missing)
	}

	assert.Equal(t, []string{"code:110101000000", "code:999999999998"}, repo.calls)

	var miss bool

	found, err := cache.Get(ctx, "region:code-miss:999999999998", &miss)
	require.NoError(t, err)
	assert.True(t, found && miss)
}

func TestQueryPrefersKeywordThenParentThenLevel(t *testing.T) {
	t.Parallel()

	parent := "110000000000"

	tests := []struct {
		name string
		q    region.Query
		call string
	}{
		{"keyword", region.Query{Level: 1, ParentCode: &parent, Keyword: "北"}, "search:北"},
		{"parent", region.Query{Level: 1, ParentCode: &parent, Keyword: ""}, "parent:110000000000"},
		{"level", region.Query{Level: 1, ParentCode: nil, Keyword: ""}, "level"},
		{"all", region.Query{Level: 0, ParentCode: nil, Keyword: ""}, "all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, repo, _ := newService()

			_, err := svc.Query(t.Context(), tt.q)
			require.NoError(t, err)
			_, err = svc.Query(t.Context(), tt.q)
			require.NoError(t, err)
			assert.Equal(t, []string{tt.call, tt.call}, repo.calls)
		})
	}
}
