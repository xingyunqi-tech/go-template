package app_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
	"github.com/xingyunqi-tech/go-template/internal/config"
)

func freePort(t *testing.T) int {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := tcpPort(t, ln)
	require.NoError(t, ln.Close())

	return port
}

func tcpPort(t *testing.T, ln net.Listener) int {
	t.Helper()

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)

	return addr.Port
}

func TestServeUntilCanceled(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load()
	require.NoError(t, err)

	cfg.App.Port = freePort(t)
	url := "http://127.0.0.1:" + strconv.Itoa(cfg.App.Port) + "/"

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx, cfg, slog.New(slog.DiscardHandler)) }()

	require.Eventually(t, func() bool {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		return resp.StatusCode == http.StatusOK && assert.JSONEq(t, `{"code":200,"message":"success","data":{"name":"app"}}`, string(body))
	}, 5*time.Second, 20*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestServeFailsWhenPortTaken(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", ":0")
	require.NoError(t, err)

	defer ln.Close()

	cfg, err := config.Load()
	require.NoError(t, err)

	cfg.App.Port = tcpPort(t, ln)

	require.Error(t, app.Serve(t.Context(), cfg, slog.New(slog.DiscardHandler)))
}
