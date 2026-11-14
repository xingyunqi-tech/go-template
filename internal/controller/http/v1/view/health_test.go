package view_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
)

func TestHealthIsTerminusShaped(t *testing.T) {
	t.Parallel()

	r := health.Result{
		Info: []health.Indicator{
			{Key: "memory_heap", Fields: nil, Up: true},
			{Key: "database", Fields: []health.Field{{Name: "responseTime", Value: int64(3)}}, Up: true},
		},
		Error: []health.Indicator{{Key: "redis", Fields: []health.Field{{Name: "message", Value: "x"}}, Up: false}},
	}

	raw, err := json.Marshal(view.NewHealth(&r))
	require.NoError(t, err)

	assert.JSONEq(t, `{"status":"error","info":{"memory_heap":{"status":"up"},"database":{"responseTime":3,"status":"up"}},`+
		`"error":{"redis":{"message":"x","status":"down"}},`+
		`"details":{"memory_heap":{"status":"up"},"database":{"responseTime":3,"status":"up"},"redis":{"message":"x","status":"down"}}}`, string(raw))
	assert.Contains(t, string(raw), `"database":{"responseTime":3,"status":"up"}`)
}

func TestEmptyIndicatorsAreObjects(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(view.NewHealth(&health.Result{Info: nil, Error: nil}))
	require.NoError(t, err)

	assert.JSONEq(t, `{"status":"ok","info":{},"error":{},"details":{}}`, string(raw))
}
