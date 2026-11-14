//go:build integration

package app_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/xingyunqi-tech/go-template/internal/testutil"
)

var _db *testutil.Database //nolint:gochecknoglobals // one container per package

func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"AUTH_JWT_SECRET":           "secret",
		"AUTH_REFRESH_SECRET":       "secret_for_refresh",
		"AUTH_FORGOT_SECRET":        "secret_for_forgot",
		"AUTH_CONFIRM_EMAIL_SECRET": "secret_for_confirm_email",
	} {
		_ = os.Setenv(k, v)
	}

	db, err := testutil.StartDatabase(context.Background())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}

	_db = db
	code := m.Run()

	db.Close()
	os.Exit(code)
}
