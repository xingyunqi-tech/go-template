package postgres

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"uuid"

	"github.com/jackc/pgx/v5"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const _userSelect = `SELECT u."id", u."email", u."password", u."provider", u."firstName", u."lastName", u."phone",
       u."nickname", u."gender", u."birthday", u."wechatOpenId", u."wechatUnionId", u."alipayOpenId",
       u."createdAt", u."updatedAt", u."deletedAt", u."roleId", r."name", u."statusId", s."name",
       f."id", f."path", f."visibility", f."mimeType", f."size", f."ownerUserId"
FROM "user" u
LEFT JOIN "role" r ON r."id" = u."roleId"
LEFT JOIN "status" s ON s."id" = u."statusId"
LEFT JOIN "file" f ON f."id" = u."photoId"
WHERE u."deletedAt" IS NULL`

// sortableUserColumns maps user properties to columns that a list may be ordered by.
func sortableUserColumns() map[string]string {
	return map[string]string{
		"id": `u."id"`, "email": `u."email"`, "password": `u."password"`, "provider": `u."provider"`,
		"firstName": `u."firstName"`, "lastName": `u."lastName"`, "phone": `u."phone"`,
		"nickname": `u."nickname"`, "gender": `u."gender"`, "birthday": `u."birthday"`,
		"wechatOpenId": `u."wechatOpenId"`, "wechatUnionId": `u."wechatUnionId"`,
		"alipayOpenId": `u."alipayOpenId"`, "createdAt": `u."createdAt"`, "updatedAt": `u."updatedAt"`,
		"deletedAt": `u."deletedAt"`,
	}
}

// Users implements repo.Users.
type Users struct {
	db *DB
}

// NewUsers returns the users repository.
func NewUsers(db *DB) *Users {
	return &Users{db: db}
}

var _ repo.Users = (*Users)(nil)

func scanUser(row pgx.Row) (*entity.User, error) {
	var (
		u                   entity.User
		gender              *int32
		roleID, statusID    *int32
		roleName, stName    *string
		fileID              *uuid.UUID
		filePath, fileVis   *string
		fileMime            *string
		fileSize, fileOwner *int32
		id                  int32
	)

	err := row.Scan(&id, &u.Email, &u.Password, &u.Provider, &u.FirstName, &u.LastName, &u.Phone,
		&u.Nickname, &gender, &u.Birthday, &u.WechatOpenID, &u.WechatUnionID, &u.AlipayOpenID,
		&u.CreatedAt, &u.UpdatedAt, &u.DeletedAt, &roleID, &roleName, &statusID, &stName,
		&fileID, &filePath, &fileVis, &fileMime, &fileSize, &fileOwner)
	if err != nil {
		return nil, err //nolint:wrapcheck // callers wrap
	}

	u.ID = int(id)
	u.Gender = intPtr(gender)
	u.Role = named(roleID, roleName)
	u.Status = named(statusID, stName)

	if fileID != nil {
		u.Photo = &entity.File{
			ID: *fileID, Path: deref(filePath), Visibility: deref(fileVis), MimeType: fileMime,
			Size: intPtr(fileSize), OwnerUserID: intPtr(fileOwner),
		}
	}

	return &u, nil
}

// Get returns the active user matching q, or nil.
func (r *Users) Get(ctx context.Context, q repo.UserQuery) (*entity.User, error) {
	var (
		column string
		arg    any
	)

	switch {
	case q.ID != nil:
		column, arg = `u."id"`, int32(*q.ID) //nolint:gosec // ids are int4
	case q.Email != nil:
		column, arg = `u."email"`, *q.Email
	case q.Phone != nil:
		column, arg = `u."phone"`, *q.Phone
	case q.WechatOpenID != nil:
		column, arg = `u."wechatOpenId"`, *q.WechatOpenID
	case q.AlipayOpenID != nil:
		column, arg = `u."alipayOpenId"`, *q.AlipayOpenID
	default:
		return nil, nil //nolint:nilnil // nothing to look up
	}

	var user *entity.User

	err := r.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		u, err := scanUser(db.QueryRow(ctx, _userSelect+" AND "+column+" = $1", arg))
		if notFound(err) {
			return nil
		}

		user = u

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("users: get: %w", err)
	}

	return user, nil
}

// Create inserts a user and returns its id.
func (r *Users) Create(ctx context.Context, u *repo.NewUser) (int, error) {
	var id int32

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		id, err = q.CreateUser(ctx, sqlcgen.CreateUserParams{
			FirstName: u.FirstName, LastName: u.LastName, Email: u.Email, Phone: u.Phone,
			Nickname: u.Nickname, Gender: int32Ptr(u.Gender), Birthday: u.Birthday, Password: u.Password,
			Provider: u.Provider, WechatOpenID: u.WechatOpenID, WechatUnionID: u.WechatUnionID,
			AlipayOpenID: u.AlipayOpenID, PhotoID: u.PhotoID, RoleID: int32Ptr(u.RoleID), StatusID: int32Ptr(u.StatusID),
		})

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("users: create: %w", err)
	}

	return int(id), nil
}

func emptyChanges(c *repo.UserChanges) bool {
	set := []bool{
		c.FirstName.Set, c.LastName.Set, c.Email.Set, c.Phone.Set, c.Nickname.Set, c.Gender.Set,
		c.Birthday.Set, c.Password.Set, c.Provider.Set, c.PhotoID.Set, c.RoleID != nil, c.StatusID != nil,
	}

	return !slices.Contains(set, true)
}

// Update applies c and reports whether an active user was updated; without changes it only
// reports whether the user exists.
func (r *Users) Update(ctx context.Context, id int, c *repo.UserChanges) (bool, error) {
	if emptyChanges(c) {
		u, err := r.Get(ctx, repo.UserQuery{ID: &id})

		return u != nil, err
	}

	var rows int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		rows, err = q.UpdateUser(ctx, sqlcgen.UpdateUserParams{
			SetFirstName: c.FirstName.Set, FirstName: c.FirstName.Value,
			SetLastName: c.LastName.Set, LastName: c.LastName.Value,
			SetEmail: c.Email.Set, Email: c.Email.Value,
			SetPhone: c.Phone.Set, Phone: c.Phone.Value,
			SetNickname: c.Nickname.Set, Nickname: c.Nickname.Value,
			SetGender: c.Gender.Set, Gender: int32Ptr(c.Gender.Value),
			SetBirthday: c.Birthday.Set, Birthday: c.Birthday.Value,
			SetPassword: c.Password.Set, Password: c.Password.Value,
			SetProvider: c.Provider.Set, Provider: c.Provider.Value,
			SetPhoto: c.PhotoID.Set, PhotoID: c.PhotoID.Value,
			RoleID: int32Ptr(c.RoleID), StatusID: int32Ptr(c.StatusID),
			ID: int32(id), //nolint:gosec // ids are int4
		})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("users: update: %w", err)
	}

	return rows > 0, nil
}

// ReplacePassword sets hash when the stored password still equals old (nil for none).
func (r *Users) ReplacePassword(ctx context.Context, id int, old *string, hash string) (bool, error) {
	var rows int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		rows, err = q.ReplacePassword(ctx, sqlcgen.ReplacePasswordParams{
			NewPassword: &hash, ID: int32(id), OldPassword: old, //nolint:gosec // ids are int4
		})

		return err
	})
	if err != nil {
		return false, fmt.Errorf("users: replace password: %w", err)
	}

	return rows > 0, nil
}

// RehashPassword swaps old for hash without touching updatedAt.
func (r *Users) RehashPassword(ctx context.Context, id int, old, hash string) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		return q.RehashPassword(ctx, sqlcgen.RehashPasswordParams{
			NewPassword: &hash, ID: int32(id), OldPassword: &old, //nolint:gosec // ids are int4
		})
	})
	if err != nil {
		return fmt.Errorf("users: rehash password: %w", err)
	}

	return nil
}

// Remove releases the unique columns and soft-deletes the user.
func (r *Users) Remove(ctx context.Context, id int) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		return q.RemoveUser(ctx, int32(id)) //nolint:gosec // ids are int4
	})
	if err != nil {
		return fmt.Errorf("users: remove: %w", err)
	}

	return nil
}

// CountByRole counts active users with the role.
func (r *Users) CountByRole(ctx context.Context, roleID int) (int, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.CountActiveUsersByRole(ctx, int32Ptr(&roleID))

		return err
	})
	if err != nil {
		return 0, fmt.Errorf("users: count: %w", err)
	}

	return int(n), nil
}

// ErrUnknownSortProperty reports a sort property that is not a user column; nestjs fails the
// request with a 500 for it.
type ErrUnknownSortProperty struct {
	Property string
}

func (e *ErrUnknownSortProperty) Error() string {
	return `Property "` + e.Property + `" was not found in "UserEntity"`
}

// List returns a page ordered by q.Sort, then by id when the sort does not include it.
func (r *Users) List(ctx context.Context, q *repo.UserList) ([]entity.User, error) {
	columns := sortableUserColumns()
	query := _userSelect
	args := []any{}

	if len(q.RoleIDs) > 0 {
		ids := make([]int32, len(q.RoleIDs))
		for i, id := range q.RoleIDs {
			ids[i] = int32(id) //nolint:gosec // role ids are small
		}

		args = append(args, ids)
		query += ` AND u."roleId" = ANY($1)`
	}

	order, err := orderBy(columns, q.Sort)
	if err != nil {
		return nil, err
	}

	args = append(args, q.Offset, q.Limit)
	query += " ORDER BY " + order +
		fmt.Sprintf(" OFFSET $%d LIMIT $%d", len(args)-1, len(args))

	var users []entity.User

	err = r.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		rows, err := db.Query(ctx, query, args...)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		defer rows.Close()

		for rows.Next() {
			u, err := scanUser(rows)
			if err != nil {
				return err
			}

			users = append(users, *u)
		}

		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("users: list: %w", err)
	}

	return users, nil
}

// orderBy follows the requested sort, then orders by id unless the sort already does.
func orderBy(columns map[string]string, sort []repo.SortField) (string, error) {
	order := make([]string, 0, len(sort)+1)
	byID := false

	for _, s := range sort {
		column, ok := columns[s.Property]
		if !ok {
			return "", &ErrUnknownSortProperty{Property: s.Property}
		}

		byID = byID || s.Property == "id"
		direction := " ASC"

		if s.Descending {
			direction = " DESC"
		}

		order = append(order, column+direction)
	}

	if !byID {
		order = append(order, `u."id" ASC`)
	}

	return strings.Join(order, ", "), nil
}

func named(id *int32, name *string) *entity.Named {
	if id == nil {
		return nil
	}

	return &entity.Named{ID: int(*id), Name: deref(name)}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}

	return *p
}

func intPtr(p *int32) *int {
	if p == nil {
		return nil
	}

	v := int(*p)

	return &v
}

func int32Ptr(p *int) *int32 {
	if p == nil {
		return nil
	}

	v := int32(*p) //nolint:gosec // int4 columns

	return &v
}
