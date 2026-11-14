package health_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
)

type fakeDB struct {
	pingErr   error
	pingDelay time.Duration
	active    int
	maximum   int
	statsErr  error
}

func (f *fakeDB) Ping(ctx context.Context) error {
	if f.pingDelay > 0 {
		select {
		case <-time.After(f.pingDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return f.pingErr
}

func (f *fakeDB) ConnectionStats(context.Context) (active, maximum int, err error) {
	return f.active, f.maximum, f.statsErr
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func status(code int) roundTrip {
	return func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: code, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	}
}

var errBoom = errors.New("boom")

func service(db *fakeDB, mutate func(*health.Deps)) *health.Service {
	deps := health.Deps{
		Config:   health.Config{WechatAppID: "", WechatAppSecret: "", AlipayAppID: ""},
		Database: db,
		Redis:    nil,
		HTTP:     nil,
		DiskPath: "/",
		Memory:   func() uint64 { return 1 },
		Disk:     func(string) (uint64, uint64, error) { return 100, 50, nil },
	}
	if mutate != nil {
		mutate(&deps)
	}

	return health.New(&deps)
}

func keys(in []health.Indicator) []string {
	out := make([]string, len(in))
	for i, ind := range in {
		out[i] = ind.Key
	}

	return out
}

func field(t *testing.T, ind health.Indicator, name string) any {
	t.Helper()

	for _, f := range ind.Fields {
		if f.Name == name {
			return f.Value
		}
	}

	t.Fatalf("%s has no %s", ind.Key, name)

	return nil
}

func TestFullCheckRunsIndicatorsInTerminusOrder(t *testing.T) {
	t.Parallel()

	r := service(&fakeDB{active: 9, maximum: 100}, func(d *health.Deps) { d.Redis = fakePinger{} }).Full(t.Context())

	require.True(t, r.OK())
	assert.Equal(t, []string{"memory_heap", "disk", "database", "database_pool", "redis", "wechat_api", "alipay_api"}, keys(r.Info))
	assert.Empty(t, r.Info[0].Fields)
	assert.Equal(t, []health.Field{
		{Name: "activeConnections", Value: 9}, {Name: "maxConnections", Value: 100}, {Name: "utilizationPercent", Value: int64(9)},
	}, r.Info[3].Fields)
	assert.Equal(t, "WeChat not configured, skipped", field(t, r.Info[5], "message"))
	assert.Equal(t, "Alipay not configured, skipped", field(t, r.Info[6], "message"))
}

func TestFullCheckSkipsRedisWhenDisabled(t *testing.T) {
	t.Parallel()

	r := service(&fakeDB{active: 1, maximum: 100}, nil).Full(t.Context())

	assert.NotContains(t, keys(r.Info), "redis")
}

func TestThresholdsMarkIndicatorsDown(t *testing.T) {
	t.Parallel()

	r := service(&fakeDB{active: 91, maximum: 100}, func(d *health.Deps) {
		d.Memory = func() uint64 { return 300*1024*1024 + 1 }
		d.Disk = func(string) (uint64, uint64, error) { return 100, 9, nil }
		d.Redis = fakePinger{err: errBoom}
	}).Full(t.Context())

	assert.False(t, r.OK())
	assert.Equal(t, []string{"memory_heap", "disk", "database_pool", "redis"}, keys(r.Error))
	assert.Equal(t, "Used heap exceeded the set threshold", field(t, r.Error[0], "message"))
	assert.Equal(t, "Used disk storage exceeded the set threshold", field(t, r.Error[1], "message"))
	assert.Equal(t, int64(91), field(t, r.Error[2], "utilizationPercent"))
}

func TestDatabasePingTimesOutAfterOneSecond(t *testing.T) {
	t.Parallel()

	r := service(&fakeDB{pingDelay: 3 * time.Second}, nil).Ready(t.Context())

	require.Len(t, r.Error, 1)
	assert.Equal(t, "timeout of 1000ms exceeded", field(t, r.Error[0], "message"))
	assert.GreaterOrEqual(t, field(t, r.Error[0], "responseTime"), int64(1000))
}

func TestDatabaseFailureCarriesMessageAndResponseTime(t *testing.T) {
	t.Parallel()

	r := service(&fakeDB{pingErr: errBoom}, nil).Ready(t.Context())

	require.Len(t, r.Error, 1)
	assert.Equal(t, "boom", field(t, r.Error[0], "message"))
	assert.Equal(t, "responseTime", r.Error[0].Fields[1].Name)
}

func TestReadyReportsDatabaseResponseTime(t *testing.T) {
	t.Parallel()

	r := service(&fakeDB{}, nil).Ready(t.Context())

	require.True(t, r.OK())
	assert.Equal(t, "responseTime", r.Info[0].Fields[0].Name)
}

func TestLiveOnlyChecksMemory(t *testing.T) {
	t.Parallel()

	r := service(&fakeDB{pingErr: errBoom}, nil).Live(t.Context())

	assert.True(t, r.OK())
	assert.Equal(t, []string{"memory_heap"}, keys(r.Info))
}

func TestExternalProbes(t *testing.T) {
	t.Parallel()

	configured := func(rt roundTrip) func(*health.Deps) {
		return func(d *health.Deps) {
			d.Config = health.Config{WechatAppID: "wx", WechatAppSecret: "s", AlipayAppID: "ali"}
			d.HTTP = &http.Client{Transport: rt}
		}
	}

	cases := []struct {
		name   string
		rt     roundTrip
		wechat health.Field
		alipay health.Field
		up     bool
	}{
		{"2xx is up", status(http.StatusOK), health.Field{Name: "responseTime", Value: "OK"}, health.Field{Name: "responseTime", Value: "OK"}, true},
		{
			"alipay accepts 405", status(http.StatusMethodNotAllowed),
			health.Field{Name: "statusCode", Value: http.StatusMethodNotAllowed},
			health.Field{Name: "responseTime", Value: "OK"},
			false,
		},
		{
			"network error is fetch failed", func(*http.Request) (*http.Response, error) { return nil, errBoom },
			health.Field{Name: "message", Value: "fetch failed"},
			health.Field{Name: "message", Value: "fetch failed"},
			false,
		},
		{
			"timeout is aborted", func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
			health.Field{Name: "message", Value: "This operation was aborted"},
			health.Field{Name: "message", Value: "This operation was aborted"},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := service(&fakeDB{active: 1, maximum: 100}, configured(tc.rt)).Full(t.Context())
			all := append(append([]health.Indicator{}, r.Info...), r.Error...)

			for _, ind := range all {
				switch ind.Key {
				case "wechat_api":
					assert.Equal(t, []health.Field{tc.wechat}, ind.Fields)
					assert.Equal(t, tc.up, ind.Up)
				case "alipay_api":
					assert.Equal(t, []health.Field{tc.alipay}, ind.Fields)
				}
			}
		})
	}
}

func TestRuntimeMemoryIsPositive(t *testing.T) {
	t.Parallel()

	assert.Positive(t, health.RuntimeMemory())
}

func TestReadyFailsWhileDraining(t *testing.T) {
	t.Parallel()

	s := service(&fakeDB{}, nil)
	s.Drain()

	r := s.Ready(t.Context())
	assert.False(t, r.OK())
	assert.Equal(t, []string{"shutdown"}, keys(r.Error))
	live := s.Live(t.Context())
	assert.True(t, live.OK())
}
