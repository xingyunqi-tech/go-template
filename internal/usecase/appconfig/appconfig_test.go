package appconfig_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appconfig"
)

var _stamp = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // fixture

type fakeConfigs struct {
	rows  map[string]*entity.Config
	reads int
}

func (f *fakeConfigs) Get(_ context.Context, key string) (*entity.Config, error) {
	f.reads++

	return f.rows[key], nil
}

func (f *fakeConfigs) Upsert(_ context.Context, key string, value []byte, isPublic *bool) (*entity.Config, error) {
	row := f.rows[key]
	if row == nil {
		row = &entity.Config{Key: key, Value: nil, IsPublic: false, CreatedAt: _stamp, UpdatedAt: _stamp}
		f.rows[key] = row
	}

	row.Value = value
	if isPublic != nil {
		row.IsPublic = *isPublic
	}

	return row, nil
}

type deferredTx struct {
	pending []func(context.Context)
}

func (d *deferredTx) AfterCommit(_ context.Context, fn func(context.Context)) {
	d.pending = append(d.pending, fn)
}

func (d *deferredTx) commit(ctx context.Context) {
	for _, fn := range d.pending {
		fn(ctx)
	}

	d.pending = nil
}

type fixture struct {
	svc     *appconfig.Service
	configs *fakeConfigs
	cache   *redis.Memory
	tx      *deferredTx
}

func newFixture(rows ...*entity.Config) *fixture {
	f := &fixture{configs: &fakeConfigs{rows: map[string]*entity.Config{}, reads: 0}, cache: redis.NewMemory(), tx: &deferredTx{pending: nil}}
	for _, r := range rows {
		f.configs.rows[r.Key] = r
	}

	f.svc = appconfig.New(f.configs, f.cache, f.tx, slog.New(slog.DiscardHandler))

	return f
}

func row(key, value string, public bool) *entity.Config {
	return &entity.Config{Key: key, Value: []byte(value), IsPublic: public, CreatedAt: _stamp, UpdatedAt: _stamp}
}

func marshal(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return string(raw)
}

func TestFindUsesDefinitionVisibilityAndDefaults(t *testing.T) {
	t.Parallel()

	f := newFixture(row("ai.enabled", `{"value": false}`, false), row("custom", `{"a": 1}`, true))
	ctx := t.Context()

	enabled, err := f.svc.FindPublic(ctx, "ai.enabled")
	require.NoError(t, err)
	assert.JSONEq(t, `{"key":"ai.enabled","value":{"value":false},"isPublic":true,"createdAt":"2026-11-10T12:00:00.000Z","updatedAt":"2026-11-10T12:00:00.000Z"}`, marshal(t, enabled))

	chat, err := f.svc.FindPublic(ctx, "ai.quota.chat")
	require.NoError(t, err)
	assert.Equal(t, `{"key":"ai.quota.chat","value":{"value":20},"isPublic":true,"createdAt":null,"updatedAt":null}`, marshal(t, chat))

	custom, err := f.svc.FindPublic(ctx, "custom")
	require.NoError(t, err)
	assert.True(t, custom.IsPublic)

	for _, key := range []string{"ai.ratePerMinute", "ai.model", "unknown"} {
		_, err := f.svc.FindPublic(ctx, key)

		var httpErr *entity.Error
		require.ErrorAs(t, err, &httpErr, key)
		assert.Equal(t, "configNotFound", httpErr.ErrorKey)
	}

	f.configs.rows["numbers"] = row("numbers", `{"a": 1.0, "b": 1e2, "c": [0.50]}`, true)

	numbers, err := f.svc.Find(ctx, "numbers")
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":100,"c":[0.5]}`, string(numbers.Value))

	rate, err := f.svc.Find(ctx, "ai.ratePerMinute")
	require.NoError(t, err)
	assert.False(t, rate.IsPublic)
	assert.JSONEq(t, `{"value":10}`, string(rate.Value))
}

func TestValueReadsValueFieldOrDefault(t *testing.T) {
	t.Parallel()

	f := newFixture(
		row("consent.privacyPolicyVersion", `{"value": "v1"}`, true),
		row("consent.userAgreementVersion", `{"other": 1}`, true),
		row("ai.quota.chat", `[1]`, true),
		row("ai.quota.grading", `{"value": null}`, true),
	)
	ctx := t.Context()

	tests := []struct {
		key  string
		want string
		ok   bool
	}{
		{"consent.privacyPolicyVersion", `"v1"`, true},
		{"consent.userAgreementVersion", "", false},
		{"ai.quota.chat", `20`, true},
		{"ai.quota.grading", `null`, true},
		{"ai.enabled", `true`, true},
		{"unknown", "", false},
	}

	for _, tt := range tests {
		got, ok, err := f.svc.Value(ctx, tt.key)
		require.NoError(t, err)
		assert.Equal(t, tt.ok, ok, tt.key)
		assert.Equal(t, tt.want, string(got), tt.key)
	}
}

func TestLoadCachesInNestjsFormat(t *testing.T) {
	t.Parallel()

	f := newFixture(row("ai.enabled", `{"value": true}`, true))
	ctx := t.Context()

	_, err := f.svc.Find(ctx, "ai.enabled")
	require.NoError(t, err)

	var cached json.RawMessage

	found, err := f.cache.Get(ctx, appconfig.CacheKey("ai.enabled", "0"), &cached)
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `{"config":{"key":"ai.enabled","value":{"value":true},"isPublic":true,"createdAt":"2026-11-10T12:00:00.000Z","updatedAt":"2026-11-10T12:00:00.000Z"}}`, string(cached))

	_, err = f.svc.Find(ctx, "ai.model")
	require.Error(t, err)

	found, err = f.cache.Get(ctx, appconfig.CacheKey("ai.model", "0"), &cached)
	require.NoError(t, err)
	require.True(t, found)
	assert.JSONEq(t, `{"config":null}`, string(cached))

	_, err = f.svc.Find(ctx, "unknown")
	require.Error(t, err)

	found, err = f.cache.Get(ctx, appconfig.CacheKey("unknown", "0"), &cached)
	require.NoError(t, err)
	assert.False(t, found)

	reads := f.configs.reads

	_, _ = f.svc.Find(ctx, "ai.enabled")
	_, _ = f.svc.Find(ctx, "ai.model")
	_, _ = f.svc.Find(ctx, "unknown")
	assert.Equal(t, reads+1, f.configs.reads)
}

func TestUpsertBumpsVersionAfterCommit(t *testing.T) {
	t.Parallel()

	f := newFixture(row("custom", `{"a": 1}`, true))
	ctx := t.Context()

	_, err := f.svc.Find(ctx, "custom")
	require.NoError(t, err)

	written, err := f.svc.Upsert(ctx, "custom", json.RawMessage(`{"b":2,"a":1}`), nil)
	require.NoError(t, err)
	assert.Equal(t, `{"b":2,"a":1}`, string(written.Value))
	assert.True(t, written.IsPublic)

	before, err := f.svc.Find(ctx, "custom")
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, string(before.Value))

	f.tx.commit(ctx)

	var version string

	found, err := f.cache.Get(ctx, appconfig.VersionKey("custom"), &version)
	require.NoError(t, err)
	require.True(t, found)
	assert.Len(t, version, 36)

	after, err := f.svc.Find(ctx, "custom")
	require.NoError(t, err)
	assert.JSONEq(t, `{"b":2,"a":1}`, string(after.Value))
}

func TestUpsertTakesVisibilityFromDefinition(t *testing.T) {
	t.Parallel()

	f := newFixture()
	public := true

	written, err := f.svc.UpsertGeneric(t.Context(), "ai.ratePerMinute", json.RawMessage(`{"value":11}`), &public)
	require.NoError(t, err)
	assert.False(t, written.IsPublic)
	assert.False(t, f.configs.rows["ai.ratePerMinute"].IsPublic)

	_, err = f.svc.UpsertGeneric(t.Context(), appconfig.KeyAppProfile, json.RawMessage(`{}`), nil)

	var httpErr *entity.Error
	require.True(t, errors.As(err, &httpErr))
	assert.Equal(t, entity.StatusUnprocessable, httpErr.Status)
	assert.Equal(t, []entity.Detail{{Name: "key", Value: "configManagedByDedicatedEndpoint"}}, httpErr.Fields)
	assert.NotContains(t, f.configs.rows, appconfig.KeyAppProfile)
}

func TestDefinitionsHaveUniqueKeys(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, d := range appconfig.Definitions() {
		assert.False(t, seen[d.Key], d.Key)
		seen[d.Key] = true

		found, ok := appconfig.Find(d.Key)
		assert.True(t, ok)
		assert.Equal(t, d.Key, found.Key)
	}

	assert.Len(t, seen, 20)
}
