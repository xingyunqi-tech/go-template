package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

// _adminLockKey is the pg_advisory_xact_lock key of the changes that may remove an admin.
const _adminLockKey int64 = 0x61646d696e // "admin"

const _uniqueViolation = "23505"

// Users implements repo.Users.
type Users struct {
	db *DB
}

// NewUsers returns Users.
func NewUsers(db *DB) *Users {
	return &Users{db: db}
}

var _ repo.Users = (*Users)(nil)

func userOf(u *sqlcgen.AppUser, roles []string) *repo.UserRecord {
	if roles == nil {
		roles = []string{}
	}

	return &repo.UserRecord{
		User: entity.User{
			ID: u.ID, Email: u.Email, EmailVerified: u.EmailVerifiedAt != nil, Phone: u.Phone, DisplayName: u.DisplayName,
			AvatarFileID: u.AvatarFileID, Status: u.Status, Roles: roles, HasPassword: u.PasswordHash != nil,
			CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		},
		PasswordHash: u.PasswordHash,
	}
}

// takenKey maps a unique violation of the email or phone index to its error.
func takenKey(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != _uniqueViolation {
		return err
	}

	switch pgErr.ConstraintName {
	case "app_user_email_key":
		return entity.Wrap(entity.KeyEmailTaken, err)
	case "app_user_phone_key":
		return entity.Wrap(entity.KeyPhoneTaken, err)
	}

	return err
}

func rolesByUser(ctx context.Context, q *sqlcgen.Queries, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	rows, err := q.ListUserRoles(ctx, ids)
	if err != nil {
		return nil, err //nolint:wrapcheck // wrapped by the caller
	}

	out := make(map[uuid.UUID][]string, len(ids))
	for _, r := range rows {
		out[r.UserID] = append(out[r.UserID], r.RoleCode)
	}

	return out, nil
}

func setRoles(ctx context.Context, q *sqlcgen.Queries, id uuid.UUID, roles []string) error {
	if err := q.TouchUser(ctx, id); err != nil {
		return err //nolint:wrapcheck // wrapped by the caller
	}

	if err := q.DeleteUserRoles(ctx, id); err != nil {
		return err //nolint:wrapcheck // wrapped by the caller
	}

	for _, role := range roles {
		if err := q.AddUserRole(ctx, sqlcgen.AddUserRoleParams{UserID: id, RoleCode: role}); err != nil {
			return err //nolint:wrapcheck // wrapped by the caller
		}
	}

	return nil
}

// Create implements repo.Users; it needs a transaction to store the user with its roles.
func (r *Users) Create(ctx context.Context, u *repo.NewUser) (*entity.User, error) {
	var out *entity.User

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
			ID: u.ID, Email: u.Email, Phone: u.Phone, PasswordHash: u.PasswordHash, DisplayName: u.DisplayName,
			EmailVerified: u.EmailVerified,
		})
		if err != nil {
			return takenKey(err)
		}

		if err := setRoles(ctx, q, row.ID, u.Roles); err != nil {
			return err
		}

		out = &userOf(row, u.Roles).User

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: create user: %w", err)
	}

	return out, nil
}

func (r *Users) one(ctx context.Context, op string, fn func(q *sqlcgen.Queries) (*sqlcgen.AppUser, error)) (*repo.UserRecord, bool, error) {
	var out *repo.UserRecord

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := fn(q)
		if err != nil {
			return err
		}

		roles, err := rolesByUser(ctx, q, []uuid.UUID{row.ID})
		if err != nil {
			return err
		}

		out = userOf(row, roles[row.ID])

		return nil
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, fmt.Errorf("postgres: %s user: %w", op, takenKey(err))
	}

	return out, true, nil
}

// Get implements repo.Users.
func (r *Users) Get(ctx context.Context, id uuid.UUID) (*repo.UserRecord, bool, error) {
	return r.one(ctx, "get", func(q *sqlcgen.Queries) (*sqlcgen.AppUser, error) { return q.GetUser(ctx, id) })
}

// GetForUpdate implements repo.Users.
func (r *Users) GetForUpdate(ctx context.Context, id uuid.UUID) (*repo.UserRecord, bool, error) {
	return r.one(ctx, "lock", func(q *sqlcgen.Queries) (*sqlcgen.AppUser, error) { return q.GetUserForUpdate(ctx, id) })
}

// GetByEmail implements repo.Users.
func (r *Users) GetByEmail(ctx context.Context, email string) (*repo.UserRecord, bool, error) {
	return r.one(ctx, "get", func(q *sqlcgen.Queries) (*sqlcgen.AppUser, error) { return q.GetUserByEmail(ctx, &email) })
}

// Update implements repo.Users.
func (r *Users) Update(ctx context.Context, id uuid.UUID, c *repo.UserChanges) (*entity.User, bool, error) {
	rec, found, err := r.one(ctx, "update", func(q *sqlcgen.Queries) (*sqlcgen.AppUser, error) {
		return q.UpdateUser(ctx, sqlcgen.UpdateUserParams{
			ID: id, DisplayName: c.DisplayName, Status: c.Status, SetAvatar: c.SetAvatar, AvatarFileID: c.AvatarFileID,
		})
	})
	if !found || err != nil {
		return nil, found, err
	}

	return &rec.User, true, nil
}

// SetPassword implements repo.Users.
func (r *Users) SetPassword(ctx context.Context, id uuid.UUID, hash string) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		_, err := q.SetUserPassword(ctx, sqlcgen.SetUserPasswordParams{ID: id, PasswordHash: &hash})

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("postgres: set password: %w", err)
	}

	return nil
}

// MarkEmailVerified implements repo.Users.
func (r *Users) MarkEmailVerified(ctx context.Context, id uuid.UUID) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		_, err := q.MarkEmailVerified(ctx, id)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("postgres: verify email: %w", err)
	}

	return nil
}

// SetRoles implements repo.Users.
func (r *Users) SetRoles(ctx context.Context, id uuid.UUID, roles []string) error {
	if err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return setRoles(ctx, q, id, roles) }); err != nil {
		return fmt.Errorf("postgres: set roles: %w", err)
	}

	return nil
}

// List implements repo.Users.
func (r *Users) List(ctx context.Context, f *repo.UserFilter) ([]entity.User, int, error) {
	var (
		users []entity.User
		total int64
	)

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		keyword := likePattern(f.Keyword)

		total, err = q.CountUsers(ctx, sqlcgen.CountUsersParams{Keyword: keyword, Status: f.Status, Role: f.Role})
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		sort := append(slices.Clone(f.Sort), "", "")

		rows, err := q.ListUsers(ctx, sqlcgen.ListUsersParams{
			Keyword: keyword, Status: f.Status, Role: f.Role, Sort1: sort[0], Sort2: sort[1],
			RowOffset: int64(f.Offset), RowLimit: int64(f.Limit),
		})
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		ids := make([]uuid.UUID, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}

		roles, err := rolesByUser(ctx, q, ids)
		if err != nil {
			return err
		}

		users = make([]entity.User, 0, len(rows))
		for _, row := range rows {
			users = append(users, userOf(row, roles[row.ID]).User)
		}

		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list users: %w", err)
	}

	return users, int(total), nil
}

// likePattern escapes the LIKE wildcards of keyword.
func likePattern(keyword *string) *string {
	if keyword == nil {
		return nil
	}

	escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(*keyword)

	return &escaped
}

// LockAdmins implements repo.Users.
func (r *Users) LockAdmins(ctx context.Context) error {
	if err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.LockAdmins(ctx, _adminLockKey) }); err != nil {
		return fmt.Errorf("postgres: lock admins: %w", err)
	}

	return nil
}

// CountActiveAdmins implements repo.Users.
func (r *Users) CountActiveAdmins(ctx context.Context, except uuid.UUID) (int, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.CountOtherActiveAdmins(ctx, except)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return 0, fmt.Errorf("postgres: count admins: %w", err)
	}

	return int(n), nil
}

// GetByPhone implements repo.Users.
func (r *Users) GetByPhone(ctx context.Context, phone string) (*repo.UserRecord, bool, error) {
	return r.one(ctx, "get", func(q *sqlcgen.Queries) (*sqlcgen.AppUser, error) { return q.GetUserByPhone(ctx, &phone) })
}

// SetPhone implements repo.Users.
func (r *Users) SetPhone(ctx context.Context, id uuid.UUID, phone string) (*entity.User, bool, error) {
	rec, found, err := r.one(ctx, "set phone", func(q *sqlcgen.Queries) (*sqlcgen.AppUser, error) {
		return q.SetUserPhone(ctx, sqlcgen.SetUserPhoneParams{ID: id, Phone: &phone})
	})
	if !found || err != nil {
		return nil, found, err
	}

	return &rec.User, true, nil
}

// SoftDelete implements repo.Users.
func (r *Users) SoftDelete(ctx context.Context, id uuid.UUID) (bool, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.SoftDeleteUser(ctx, id)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return false, fmt.Errorf("postgres: delete user: %w", err)
	}

	return n > 0, nil
}

// Deleted implements repo.Users.
func (r *Users) Deleted(ctx context.Context, id uuid.UUID) (deleted, found bool, err error) {
	err = r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		deleted, err = q.UserDeleted(ctx, id)

		return err //nolint:wrapcheck // wrapped below
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, false, nil
	case err != nil:
		return false, false, fmt.Errorf("postgres: user deleted: %w", err)
	}

	return deleted, true, nil
}

// Purge implements repo.Users.
func (r *Users) Purge(ctx context.Context, id uuid.UUID) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		_, err := q.PurgeUser(ctx, id)

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("postgres: purge user: %w", err)
	}

	return nil
}
