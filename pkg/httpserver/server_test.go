package httpserver_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/pkg/httpserver"
)

func TestShutdownWaitsForInFlightRequest(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release

		_, _ = w.Write([]byte("done"))
	})

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := httpserver.New(ln.Addr().String(), handler)
	served := make(chan error, 1)

	go func() { served <- srv.Serve(ln) }()

	body := make(chan string, 1)

	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String(), http.NoBody)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			body <- err.Error()

			return
		}
		defer resp.Body.Close()

		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()

	<-started

	shutdown := make(chan error, 1)

	go func() { shutdown <- srv.Shutdown(context.Background()) }()

	time.Sleep(50 * time.Millisecond)
	close(release)

	require.NoError(t, <-shutdown)
	require.NoError(t, <-served)
	assert.Equal(t, "done", <-body)
}

func TestShutdownTimeout(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	defer close(release)

	started := make(chan struct{})
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := httpserver.New(ln.Addr().String(), handler)

	go func() { _ = srv.Serve(ln) }()

	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+ln.Addr().String(), http.NoBody)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()

	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	require.ErrorIs(t, srv.Shutdown(ctx), context.DeadlineExceeded)
}
