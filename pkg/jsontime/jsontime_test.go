package jsontime_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/jsontime"
)

func TestMarshalJSON(t *testing.T) {
	t.Parallel()

	shanghai := time.FixedZone("CST", 8*60*60)

	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"utc whole second", time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC), `"2026-11-10T12:00:00.000Z"`},
		{"offset converted to utc", time.Date(2026, 11, 10, 20, 0, 0, 0, shanghai), `"2026-11-10T12:00:00.000Z"`},
		{"nanoseconds truncated to millis", time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC), `"2026-01-02T03:04:05.123Z"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(jsontime.New(tt.in))
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestUnmarshalJSON(t *testing.T) {
	t.Parallel()

	var got jsontime.Time
	require.NoError(t, json.Unmarshal([]byte(`"2026-11-10T20:00:00.5+08:00"`), &got))
	assert.True(t, got.Equal(time.Date(2026, 11, 10, 12, 0, 0, 500_000_000, time.UTC)))
	assert.Equal(t, "2026-11-10T12:00:00.500Z", got.String())

	var null jsontime.Time
	require.NoError(t, json.Unmarshal([]byte(`null`), &null))
	assert.True(t, null.IsZero())

	require.Error(t, json.Unmarshal([]byte(`"yesterday"`), &got))
}
