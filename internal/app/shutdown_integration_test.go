//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/app"
)

func TestServeFinishesInFlightRequestsAfterCancel(t *testing.T) {
	t.Parallel()

	cfg := loadConfig(t, _db.Clone(t))
	cfg.File.LocalRoot = t.TempDir()
	cfg.App.ShutdownTimeout = 10 * time.Second
	cfg.App.DrainPeriod = time.Second

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr, ok := ln.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.NoError(t, ln.Close())

	cfg.App.Port = addr.Port
	base := "http://127.0.0.1:" + strconv.Itoa(cfg.App.Port)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- app.Serve(ctx, cfg, slog.New(slog.DiscardHandler)) }()

	var token string

	require.Eventually(t, func() bool {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/api/v1/auth/email/login",
			strings.NewReader(`{"email":"john.doe@example.com","password":"`+_secret00+`"}`))
		req.Header.Set("Content-Type", "application/json")

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()

		var body struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}

		_ = json.NewDecoder(res.Body).Decode(&body)
		token = body.Data.Token

		return res.StatusCode == http.StatusOK
	}, 10*time.Second, 20*time.Millisecond)

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/api/v1/files/upload", pr)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)

	status := make(chan int, 1)

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}

	go func() {
		res, err := client.Do(req)
		if err != nil {
			status <- 0

			return
		}
		defer res.Body.Close()

		status <- res.StatusCode
	}()

	part, err := mw.CreateFormFile("file", "slow.png")
	require.NoError(t, err)
	_, err = part.Write(make([]byte, 64*1024))
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	cancel()

	require.Eventually(t, func() bool {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/api/v1/health/ready", http.NoBody)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()

		return res.StatusCode == http.StatusServiceUnavailable
	}, 900*time.Millisecond, 20*time.Millisecond, "readiness fails during the drain period")

	time.Sleep(200 * time.Millisecond)

	select {
	case err := <-done:
		t.Fatalf("server stopped with a request in flight: %v", err)
	default:
	}

	_, err = part.Write(_pngBytes)
	require.NoError(t, err)
	require.NoError(t, mw.Close())
	require.NoError(t, pw.Close())

	assert.Equal(t, http.StatusCreated, <-status)
	require.NoError(t, <-done)
}
