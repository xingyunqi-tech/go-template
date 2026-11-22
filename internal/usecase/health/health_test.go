package health_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func TestReadyChecksEveryDependency(t *testing.T) {
	t.Parallel()

	ok := health.New(pinger{}, pinger{})
	assert.Equal(t, health.Report{Ready: true, Checks: map[string]string{"database": "ok", "redis": "ok"}}, ok.Ready(t.Context()))

	down := health.New(pinger{}, pinger{err: errors.New("down")})
	assert.Equal(t, health.Report{Ready: false, Checks: map[string]string{"database": "ok", "redis": "error"}}, down.Ready(t.Context()))

	noRedis := health.New(pinger{}, nil)
	assert.Equal(t, map[string]string{"database": "ok"}, noRedis.Ready(t.Context()).Checks)
}

func TestDrainingIsNotReady(t *testing.T) {
	t.Parallel()

	s := health.New(pinger{}, nil)
	s.Drain()

	assert.False(t, s.Ready(t.Context()).Ready)
}
