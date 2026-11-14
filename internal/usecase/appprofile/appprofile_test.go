package appprofile_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appprofile"
)

type store struct {
	value    []byte
	isPublic *bool
	writes   int
}

func (s *store) Get(_ context.Context, key string) (*entity.Config, error) {
	if s.value == nil {
		return nil, nil //nolint:nilnil // absent entry
	}

	return &entity.Config{Key: key, Value: s.value, IsPublic: false, CreatedAt: time.Time{}, UpdatedAt: time.Time{}}, nil
}

func (s *store) Upsert(ctx context.Context, key string, value []byte, isPublic *bool) (*entity.Config, error) {
	s.value, s.isPublic = value, isPublic
	s.writes++

	return s.Get(ctx, key)
}

type now struct{}

func (now) AfterCommit(ctx context.Context, fn func(context.Context)) { fn(ctx) }

func newService(s *store) *appprofile.Service {
	return appprofile.New(appconfig.New(s, redis.NewMemory(), now{}, slog.New(slog.DiscardHandler)))
}

func strPtr(s string) *string {
	return &s
}

func encode(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return string(raw)
}

func TestGetReadsTrimmedStrings(t *testing.T) {
	t.Parallel()

	empty, err := newService(&store{value: nil, isPublic: nil, writes: 0}).Get(t.Context())
	require.NoError(t, err)
	assert.Equal(t, `{"appName":null,"slogan":null,"companyName":null,"copyright":null,"icpLicense":null,`+
		`"servicePhone":null,"serviceHours":null,"serviceEmail":null,"shareTitle":null}`, encode(t, empty))

	stored := &store{value: []byte(`{"value": {"appName": " A\n", "slogan": 1, "copyright": "  ", "shareTitle": null}}`), isPublic: nil, writes: 0}

	p, err := newService(stored).Get(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "A", *p.AppName)
	assert.Nil(t, p.Slogan)
	assert.Nil(t, p.Copyright)
	assert.Nil(t, p.ShareTitle)

	notObject, err := newService(&store{value: []byte(`{"value": "x"}`), isPublic: nil, writes: 0}).Get(t.Context())
	require.NoError(t, err)
	assert.Nil(t, notObject.AppName)
}

func TestUpdateReplacesWholeProfile(t *testing.T) {
	t.Parallel()

	s := &store{value: []byte(`{"value": {"slogan": "old"}}`), isPublic: nil, writes: 0}
	svc := newService(s)

	p, err := svc.Update(t.Context(), appprofile.Profile{AppName: strPtr(" 备考 "), Slogan: nil, CompanyName: strPtr("   ")}) //nolint:exhaustruct // unset fields
	require.NoError(t, err)
	assert.Equal(t, "备考", *p.AppName)
	assert.Nil(t, p.CompanyName)

	assert.Equal(t, 1, s.writes)
	assert.False(t, *s.isPublic)
	assert.JSONEq(t, `{"value":{"appName":"备考","slogan":null,"companyName":null,"copyright":null,"icpLicense":null,`+
		`"servicePhone":null,"serviceHours":null,"serviceEmail":null,"shareTitle":null}}`, string(s.value))

	got, err := svc.Get(t.Context())
	require.NoError(t, err)
	assert.Equal(t, p, got)
}
