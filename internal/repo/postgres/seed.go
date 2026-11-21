package postgres

import (
	"context"
	"crypto/md5" //nolint:gosec // seed passwords are sent as MD5 like the clients do
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const (
	_demoPassword    = "Secret00"
	_uniqueViolation = "23505"
)

// SeedData is seeds/seed.json.
type SeedData struct {
	Roles       []namedRow `json:"roles"`
	Statuses    []namedRow `json:"statuses"`
	Permissions []struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Description *string `json:"description"`
		Resource    string  `json:"resource"`
		Action      string  `json:"action"`
	} `json:"permissions"`
	RolePermissions []struct {
		RoleID       int32  `json:"roleId"`
		PermissionID string `json:"permissionId"`
	} `json:"rolePermissions"`
}

type namedRow struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
}

// SeedOptions selects the accounts created by Seed.
type SeedOptions struct {
	Production    bool
	AdminEmail    string
	AdminPassword string
	Hash          func(ctx context.Context, md5 string) (string, error)
}

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s)) //nolint:gosec // see import

	return hex.EncodeToString(sum[:])
}

// Seed inserts the reference data and demo accounts like the nestjs seeds; existing rows stay.
func (d *DB) Seed(ctx context.Context, data []byte, opts SeedOptions) error {
	var seed SeedData
	if err := json.Unmarshal(data, &seed); err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	ctx, tx, err := d.Begin(ctx)
	if err != nil {
		return err
	}

	if err := d.seed(ctx, &seed, opts); err != nil {
		tx.Rollback(ctx)

		return err
	}

	if _, err := tx.Commit(ctx); err != nil {
		return err
	}

	return nil
}

func (d *DB) seed(ctx context.Context, seed *SeedData, opts SeedOptions) error {
	return d.queries(ctx, func(q *sqlcgen.Queries) error {
		if err := seedReference(ctx, q, seed); err != nil {
			return err
		}

		return d.seedUsers(ctx, q, opts)
	})
}

func seedReference(ctx context.Context, q *sqlcgen.Queries, seed *SeedData) error {
	for _, r := range seed.Roles {
		if err := q.SeedRole(ctx, sqlcgen.SeedRoleParams{ID: r.ID, Name: r.Name}); err != nil {
			return fmt.Errorf("seed: role: %w", err)
		}
	}

	for _, s := range seed.Statuses {
		if err := q.SeedStatus(ctx, sqlcgen.SeedStatusParams{ID: s.ID, Name: s.Name}); err != nil {
			return fmt.Errorf("seed: status: %w", err)
		}
	}

	for _, p := range seed.Permissions {
		if err := q.SeedPermission(ctx, sqlcgen.SeedPermissionParams{
			ID: p.ID, Name: p.Name, Description: p.Description, Resource: p.Resource, Action: p.Action,
		}); err != nil {
			return fmt.Errorf("seed: permission: %w", err)
		}
	}

	for _, rp := range seed.RolePermissions {
		if err := q.SeedRolePermission(ctx, sqlcgen.SeedRolePermissionParams{RoleID: rp.RoleID, PermissionID: rp.PermissionID}); err != nil {
			return fmt.Errorf("seed: role permission: %w", err)
		}
	}

	return nil
}

func (d *DB) seedUser(ctx context.Context, q *sqlcgen.Queries, opts SeedOptions, account [4]string, role int) error {
	hash, err := opts.Hash(ctx, md5Hex(account[3]))
	if err != nil {
		return fmt.Errorf("seed: hash: %w", err)
	}

	roleID, statusID, gender := int32(role), int32(entity.StatusActive), int32(0) //nolint:gosec // small constants
	first, last, email := account[0], account[1], account[2]

	_, err = q.CreateUser(ctx, sqlcgen.CreateUserParams{
		FirstName: &first, LastName: &last, Email: &email, Password: &hash, Provider: entity.ProviderEmail,
		RoleID: &roleID, StatusID: &statusID, Gender: &gender, Phone: nil, Nickname: nil, Birthday: nil,
		WechatOpenID: nil, WechatUnionID: nil, AlipayOpenID: nil, PhotoID: nil,
	})
	if err != nil {
		return fmt.Errorf("seed: user: %w", err)
	}

	return nil
}

func (d *DB) seedUsers(ctx context.Context, q *sqlcgen.Queries, opts SeedOptions) error {
	admin := int32(entity.RoleAdmin)
	user := int32(entity.RoleUser)

	admins, err := q.CountActiveUsersByRole(ctx, &admin)
	if err != nil {
		return fmt.Errorf("seed: users: %w", err)
	}

	email, pass := "admin@example.com", _demoPassword
	if opts.Production {
		email, pass = strings.ToLower(strings.TrimSpace(opts.AdminEmail)), opts.AdminPassword
	}

	if admins == 0 && email != "" && pass != "" {
		if err := d.seedUser(ctx, q, opts, [4]string{"Super", "Admin", email, pass}, entity.RoleAdmin); err != nil {
			return err
		}
	}

	users, err := q.CountActiveUsersByRole(ctx, &user)
	if err != nil {
		return fmt.Errorf("seed: users: %w", err)
	}

	if users == 0 && !opts.Production {
		return d.seedUser(ctx, q, opts, [4]string{"John", "Doe", "john.doe@example.com", _demoPassword}, entity.RoleUser)
	}

	return nil
}

// ErrUserExists is returned by CreateSuperuser when the email is taken.
var ErrUserExists = errors.New("seed: a user with this email already exists")

// CreateSuperuser creates an active administrator whose stored password is the hash of
// md5(password), as clients send it.
func (d *DB) CreateSuperuser(ctx context.Context, email, password string, hash func(context.Context, string) (string, error)) error {
	return d.InTx(ctx, func(ctx context.Context) error {
		return d.queries(ctx, func(q *sqlcgen.Queries) error {
			err := d.seedUser(ctx, q, SeedOptions{Hash: hash}, [4]string{"Super", "Admin", email, password}, entity.RoleAdmin)

			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == _uniqueViolation {
				return ErrUserExists
			}

			return err
		})
	})
}
