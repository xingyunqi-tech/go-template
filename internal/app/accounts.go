package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"uuid"

	"github.com/xingyunqi-tech/go-template/internal/config"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres"
	"github.com/xingyunqi-tech/go-template/internal/repo/redis"
	"github.com/xingyunqi-tech/go-template/internal/usecase/session"
	"github.com/xingyunqi-tech/go-template/pkg/password"
	"github.com/xingyunqi-tech/go-template/seeds"
)

const _seedConnections = 2

var errVerifierAccounts = errors.New("app: a verifier (AUTH_JWKS_URL) has no accounts")

// account is a seed.json entry.
type account struct {
	Email       string   `json:"email"`
	Password    string   `json:"password"`
	DisplayName string   `json:"displayName"`
	Roles       []string `json:"roles"`
}

// accounts creates users with verified emails and grants roles.
type accounts struct {
	db         *postgres.DB
	users      *postgres.Users
	revocation *session.Revocation
	hasher     *password.Hasher
	log        *slog.Logger
}

func openAccounts(ctx context.Context, cfg *config.Config, log *slog.Logger) (*accounts, func(), error) {
	if cfg.Auth.Role() == config.RoleVerifier {
		return nil, nil, errVerifierAccounts
	}

	infra, err := OpenInfra(ctx, cfg, _seedConnections)
	if err != nil {
		return nil, nil, err
	}

	hasher, err := password.New(password.Default(), cfg.Auth.HashConcurrency)
	if err != nil {
		infra.Close()

		return nil, nil, fmt.Errorf("app: %w", err)
	}

	db := postgres.NewDB(infra.Pool, _acquireTimeout, log)
	revocation := session.NewRevocation(postgres.NewSessions(db), redis.NewRevocations(infra.Redis), cfg.Auth.AccessTTL)

	return &accounts{db: db, users: postgres.NewUsers(db), revocation: revocation, hasher: hasher, log: log}, infra.Close, nil
}

// ensure creates a; an existing email gets the roles of a added and, when replace is true, its
// password replaced, its account activated and verified and its sessions signed out. created
// reports a new user.
func (s *accounts) ensure(ctx context.Context, a *account, replace bool) (created bool, err error) {
	email := entity.NormalizeEmail(a.Email)

	hash, err := s.hasher.Hash(ctx, a.Password)
	if err != nil {
		return false, fmt.Errorf("app: %w", err)
	}

	err = s.db.InTx(ctx, func(ctx context.Context) error {
		rec, found, err := s.users.GetByEmail(ctx, email)
		if err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}

		if !found {
			created = true

			_, err := s.users.Create(ctx, &repo.NewUser{
				ID: uuid.NewV7(), Email: &email, Phone: nil, PasswordHash: &hash, DisplayName: a.DisplayName,
				EmailVerified: true, Roles: a.Roles,
			})

			return err //nolint:wrapcheck // repo errors are wrapped
		}

		roles := slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(rec.User.Roles), a.Roles...))))
		if err := s.users.SetRoles(ctx, rec.User.ID, roles); err != nil {
			return err //nolint:wrapcheck // repo errors are wrapped
		}

		if !replace {
			return nil
		}

		return s.promote(ctx, rec.User.ID, hash)
	})

	return created, err //nolint:wrapcheck // repo errors are wrapped
}

func (s *accounts) promote(ctx context.Context, id uuid.UUID, hash string) error {
	if err := s.users.SetPassword(ctx, id, hash); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	active := entity.UserActive
	if _, _, err := s.users.Update(ctx, id, &repo.UserChanges{DisplayName: nil, Status: &active}); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if err := s.users.MarkEmailVerified(ctx, id); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.revocation.All(ctx, id, nil) //nolint:wrapcheck // entity or repo errors
}

// Seed creates the demo accounts of seeds/seed.json outside production; in production it
// creates the SEED_ADMIN_EMAIL administrator when no active admin exists. Existing accounts keep
// their password.
func Seed(ctx context.Context, cfg *config.Config, log *slog.Logger, _ *Observability) error {
	s, closeInfra, err := openAccounts(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeInfra()

	list, err := s.seedList(ctx, cfg)
	if err != nil {
		return err
	}

	rows := 0

	for i := range list {
		created, err := s.ensure(ctx, &list[i], false)
		if err != nil {
			return err
		}

		if created {
			rows++
		}
	}

	log.InfoContext(ctx, "seed completed", "rows", rows)

	return nil
}

func (s *accounts) seedList(ctx context.Context, cfg *config.Config) ([]account, error) {
	if !cfg.App.IsProduction() {
		var data struct {
			Accounts []account `json:"accounts"`
		}

		if err := json.Unmarshal(seeds.Data, &data); err != nil {
			return nil, fmt.Errorf("app: seed.json: %w", err)
		}

		return data.Accounts, nil
	}

	if cfg.Seed.AdminEmail == "" || cfg.Seed.AdminPassword == "" {
		return nil, nil
	}

	admins, err := s.users.CountActiveAdmins(ctx, uuid.Nil())
	if err != nil || admins > 0 {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return []account{{
		Email: cfg.Seed.AdminEmail, Password: cfg.Seed.AdminPassword.Value(), DisplayName: "Admin",
		Roles: []string{entity.RoleAdmin, entity.RoleUser},
	}}, nil
}

// CreateSuperuser creates an administrator with a verified email, or makes the existing account
// of email one and replaces its password.
func CreateSuperuser(email, pw string) func(context.Context, *config.Config, *slog.Logger, *Observability) error {
	return func(ctx context.Context, cfg *config.Config, log *slog.Logger, _ *Observability) error {
		s, closeInfra, err := openAccounts(ctx, cfg, log)
		if err != nil {
			return err
		}
		defer closeInfra()

		created, err := s.ensure(ctx, &account{
			Email: email, Password: pw, DisplayName: "Admin", Roles: []string{entity.RoleAdmin, entity.RoleUser},
		}, true)
		if err != nil {
			return err
		}

		log.InfoContext(ctx, "superuser ready", "email", email, "created", created)

		return nil
	}
}
