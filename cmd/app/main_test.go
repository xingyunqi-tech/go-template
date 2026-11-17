package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHealthcheckRejectsUnknownFlags(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"healthcheck", "--bogus"}, strings.NewReader(""), &stdout, &stderr))
}

func TestUnknownCommandPrintsUsage(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"nope"}, strings.NewReader(""), &stdout, &stderr))
	assert.Contains(t, stderr.String(), "healthcheck [--ready]")
}

func TestCreateSuperuserValidatesInputBeforeConnecting(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		args  []string
		stdin string
	}{
		"missing email":  {[]string{"createsuperuser"}, "Secret00\n"},
		"invalid email":  {[]string{"createsuperuser", "-email", "not-an-email"}, "Secret00\n"},
		"no tld":         {[]string{"createsuperuser", "-email", "admin@localhost"}, "Secret00\n"},
		"short password": {[]string{"createsuperuser", "-email", "root@example.com"}, "abc\n"},
		"empty stdin":    {[]string{"createsuperuser", "-email", "root@example.com"}, ""},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			assert.Equal(t, exitUsage, run(c.args, strings.NewReader(c.stdin), &stdout, &stderr))
		})
	}
}

func TestPermissionsRequiresClearCache(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	assert.Equal(t, exitUsage, run([]string{"permissions"}, strings.NewReader(""), &stdout, &stderr))
}
