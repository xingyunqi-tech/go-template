// Package testutil holds fixtures shared by integration tests.
package testutil

import (
	"os"
	"path/filepath"
)

// UseDockerDesktopSocket points DOCKER_HOST at Docker Desktop's per-user socket when DOCKER_HOST is
// unset and the default /var/run/docker.sock does not exist.
func UseDockerDesktopSocket() {
	if os.Getenv("DOCKER_HOST") != "" {
		return
	}

	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return
	}

	sock := filepath.Join(home, ".docker", "run", "docker.sock")
	if _, err := os.Stat(sock); err == nil {
		_ = os.Setenv("DOCKER_HOST", "unix://"+sock)
	}
}
