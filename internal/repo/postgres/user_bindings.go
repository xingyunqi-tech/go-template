package postgres

import (
	"context"
	"fmt"

	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

var _ repo.UserBindings = (*Users)(nil)

// BindPhoneIfEmpty sets the phone of an active user without one.
func (r *Users) BindPhoneIfEmpty(ctx context.Context, id int, phone string) (bool, error) {
	var rows int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		rows, err = q.BindPhoneIfEmpty(ctx, sqlcgen.BindPhoneIfEmptyParams{Phone: &phone, ID: id32(id)})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("users: bind phone: %w", err)
	}

	return rows > 0, nil
}

// BindWechatIfEmpty sets the WeChat ids of an active user without an open id.
func (r *Users) BindWechatIfEmpty(ctx context.Context, id int, openID string, unionID *string) (bool, error) {
	var rows int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		rows, err = q.BindWechatIfEmpty(ctx, sqlcgen.BindWechatIfEmptyParams{
			OpenID: &openID, UnionID: unionID, ID: id32(id),
		})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("users: bind wechat: %w", err)
	}

	return rows > 0, nil
}
