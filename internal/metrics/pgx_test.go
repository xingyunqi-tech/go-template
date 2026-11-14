package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/metrics"
)

func TestQueryLabels(t *testing.T) {
	t.Parallel()

	cases := map[string][2]string{
		"-- name: GetUser :one\nSELECT * FROM \"user\" WHERE id = $1": {"select", "user"},
		`INSERT INTO "file" ("path") VALUES ($1)`:                     {"insert", "file"},
		"update session set x = 1":                                    {"update", "session"},
		`DELETE FROM river.river_job WHERE id = $1`:                   {"delete", "river.river_job"},
		"SELECT 1": {"select", "unknown"},
		"WITH f AS (SELECT 1) SELECT * FROM f JOIN \"role\" r ON true": {"with", "f"},
	}

	for sql, want := range cases {
		op, table := metrics.QueryLabels(sql)
		assert.Equal(t, want, [2]string{op, table}, sql)
	}
}
