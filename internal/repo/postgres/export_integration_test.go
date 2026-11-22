//go:build integration

package postgres

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// Exec runs sql on the request transaction of ctx, or on a pooled connection without one.
func Exec(ctx context.Context, d *DB, sql string, args ...any) error {
	return d.dbtx(ctx, func(db sqlcgen.DBTX) error {
		_, err := db.Exec(ctx, sql, args...)

		return err
	})
}
