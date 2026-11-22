package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthcheckRejectsUnknownFlags(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"healthcheck", "--bogus"}, &stdout, &stderr))
}

func TestUnknownCommandPrintsUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"nope"}, &stdout, &stderr))
	assert.Contains(t, stderr.String(), "healthcheck [--ready]")
}

func TestVersionPrintsTheBuildVersion(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitOK, run([]string{"version"}, &stdout, &stderr))
	assert.Equal(t, "dev\n", stdout.String())
}
