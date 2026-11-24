package app

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/xingyunqi-tech/go-template/internal/events"
)

type flakySubscriber struct {
	calls  atomic.Int32
	failed int32
	after  func()
}

func (s *flakySubscriber) Subscribe(context.Context, string, events.Handler) error {
	n := s.calls.Add(1)
	if s.after != nil {
		s.after()
	}

	if n <= s.failed {
		return errors.New("broker down")
	}

	return nil
}

func TestSubscribeRetriesAFailedSubscription(t *testing.T) {
	t.Parallel()

	sub := &flakySubscriber{failed: 1}

	subscribe(t.Context(), sub, nil, slog.New(slog.DiscardHandler))
	assert.Equal(t, int32(2), sub.calls.Load())
}

func TestSubscribeStopsWithTheContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	sub := &flakySubscriber{failed: 100, after: cancel}

	done := make(chan struct{})

	go func() {
		subscribe(ctx, sub, nil, slog.New(slog.DiscardHandler))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("subscribe kept retrying after cancel")
	}

	assert.Equal(t, int32(1), sub.calls.Load())
}
