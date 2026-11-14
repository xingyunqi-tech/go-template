//go:build e2e

package e2e_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProbesReportReady(t *testing.T) {
	t.Parallel()

	for _, path := range []string{"/api/v1/health/live", "/api/v1/health/ready"} {
		status, data := call(t, http.MethodGet, path, "", nil)

		assert.Equal(t, http.StatusOK, status, path)
		assert.Equal(t, "ok", data["status"], path)
	}
}

func TestMonitoringNeedsTokenInProduction(t *testing.T) {
	t.Parallel()

	status, _ := call(t, http.MethodGet, "/api/v1/health", "", nil)
	assert.Equal(t, http.StatusForbidden, status)
}
