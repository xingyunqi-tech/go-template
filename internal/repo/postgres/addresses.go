package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/repo/postgres/sqlcgen"
)

const _addressSelect = `SELECT "id", "userId", "name", "phone", "province", "city", "district", "provinceCode",
       "cityCode", "districtCode", "address", "isDefault", "createdAt", "updatedAt", "deletedAt"
FROM "address"
WHERE "userId" = $1 AND "deletedAt" IS NULL`

func sortableAddressColumns() map[string]string {
	return map[string]string{
		"id": `"id"`, "name": `"name"`, "isDefault": `"isDefault"`, "createdAt": `"createdAt"`, "updatedAt": `"updatedAt"`,
	}
}

func defaultAddressOrder() []repo.SortField {
	return []repo.SortField{{Property: "isDefault", Descending: true}, {Property: "createdAt", Descending: true}}
}

// Addresses implements repo.Addresses.
type Addresses struct {
	db *DB
}

// NewAddresses returns the addresses repository.
func NewAddresses(db *DB) *Addresses {
	return &Addresses{db: db}
}

var _ repo.Addresses = (*Addresses)(nil)

func toAddress(a *sqlcgen.Address) *entity.Address {
	return &entity.Address{
		ID: int(a.ID), UserID: int(a.UserId), Name: a.Name, Phone: a.Phone,
		ProvinceCode: a.ProvinceCode, CityCode: a.CityCode, DistrictCode: a.DistrictCode,
		Province: a.Province, City: a.City, District: a.District, Address: a.Address, IsDefault: a.IsDefault,
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, DeletedAt: a.DeletedAt,
	}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

// atomic runs fn in the request transaction, or in a transaction of its own without one.
func (r *Addresses) atomic(ctx context.Context, fn func(q *sqlcgen.Queries) error) error {
	if stateOf(ctx) != nil {
		return r.db.queries(ctx, fn)
	}

	return r.db.InTx(ctx, func(ctx context.Context) error { return r.db.queries(ctx, fn) })
}

func countParams(userID int, f *repo.AddressFilter) sqlcgen.CountUserAddressesParams {
	return sqlcgen.CountUserAddressesParams{
		UserID: id32(userID), Province: nonEmpty(f.Province), City: nonEmpty(f.City),
		ProvinceCode: nonEmpty(f.ProvinceCode), CityCode: nonEmpty(f.CityCode), DistrictCode: nonEmpty(f.DistrictCode),
	}
}

// CreateWithinLimit implements repo.Addresses.
func (r *Addresses) CreateWithinLimit(ctx context.Context, a *repo.NewAddress, limit int) (*entity.Address, int, error) {
	var (
		created  *entity.Address
		previous int64
	)

	err := r.atomic(ctx, func(q *sqlcgen.Queries) error {
		if err := q.LockUserAddresses(ctx, id32(a.UserID)); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		var err error

		previous, err = q.CountUserAddresses(ctx, countParams(a.UserID, &repo.AddressFilter{}))
		if err != nil || previous >= int64(limit) {
			return err //nolint:wrapcheck // wrapped below
		}

		row, err := q.CreateAddress(ctx, sqlcgen.CreateAddressParams{
			UserID: id32(a.UserID), Name: a.Name, Phone: a.Phone, ProvinceCode: a.ProvinceCode,
			CityCode: a.CityCode, DistrictCode: a.DistrictCode, Province: a.Province, City: a.City,
			District: a.District, Address: a.Address,
		})
		if err == nil {
			created = toAddress(row)
		}

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, 0, fmt.Errorf("addresses: create: %w", err)
	}

	return created, int(previous), nil
}

func addressFilterSQL(f *repo.AddressFilter, args []any) (where string, out []any) {
	var sb strings.Builder

	for _, c := range []struct{ column, value string }{
		{`"province"`, f.Province},
		{`"city"`, f.City},
		{`"provinceCode"`, f.ProvinceCode},
		{`"cityCode"`, f.CityCode},
		{`"districtCode"`, f.DistrictCode},
	} {
		if c.value == "" {
			continue
		}

		args = append(args, c.value)
		fmt.Fprintf(&sb, " AND %s = $%d", c.column, len(args))
	}

	return sb.String(), args
}

// addressOrder follows the requested sort and then the default order for properties it leaves out.
func addressOrder(sort []repo.SortField) (string, error) {
	columns := sortableAddressColumns()
	seen := map[string]bool{}

	fields := append([]repo.SortField(nil), sort...)

	for _, s := range sort {
		seen[s.Property] = true
	}

	for _, d := range defaultAddressOrder() {
		if !seen[d.Property] {
			fields = append(fields, d)
		}
	}

	order := make([]string, 0, len(fields))

	for _, s := range fields {
		column, ok := columns[s.Property]
		if !ok {
			return "", fmt.Errorf("addresses: %w", &ErrUnknownSortProperty{Property: s.Property})
		}

		direction := " ASC"
		if s.Descending {
			direction = " DESC"
		}

		order = append(order, column+direction)
	}

	return strings.Join(order, ", "), nil
}

// List implements repo.Addresses.
func (r *Addresses) List(ctx context.Context, q *repo.AddressList) ([]entity.Address, error) {
	order, err := addressOrder(q.Sort)
	if err != nil {
		return nil, err
	}

	where, args := addressFilterSQL(&q.Filter, []any{id32(q.UserID)})
	args = append(args, q.Offset, q.Limit)
	query := _addressSelect + where + " ORDER BY " + order + fmt.Sprintf(" OFFSET $%d LIMIT $%d", len(args)-1, len(args))

	var out []entity.Address

	err = r.db.dbtx(ctx, func(db sqlcgen.DBTX) error {
		rows, err := db.Query(ctx, query, args...)
		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}
		defer rows.Close()

		for rows.Next() {
			var a sqlcgen.Address
			if err := rows.Scan(&a.ID, &a.UserId, &a.Name, &a.Phone, &a.Province, &a.City, &a.District,
				&a.ProvinceCode, &a.CityCode, &a.DistrictCode, &a.Address, &a.IsDefault, &a.CreatedAt,
				&a.UpdatedAt, &a.DeletedAt); err != nil {
				return err //nolint:wrapcheck // wrapped below
			}

			out = append(out, *toAddress(&a))
		}

		return rows.Err() //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("addresses: list: %w", err)
	}

	return out, nil
}

// Count implements repo.Addresses.
func (r *Addresses) Count(ctx context.Context, userID int, f *repo.AddressFilter) (int, error) {
	var n int64

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		var err error

		n, err = q.CountUserAddresses(ctx, countParams(userID, f))

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return 0, fmt.Errorf("addresses: count: %w", err)
	}

	return int(n), nil
}

// findRow converts the row get returns; a missing row is nil.
func findRow[R, T any](ctx context.Context, db *DB, get func(q *sqlcgen.Queries) (*R, error), conv func(*R) *T) (*T, error) {
	var out *T

	err := db.queries(ctx, func(q *sqlcgen.Queries) error {
		row, err := get(q)
		if notFound(err) {
			return nil
		}

		if err == nil {
			out = conv(row)
		}

		return err
	})

	return out, err
}

func (r *Addresses) one(ctx context.Context, name string, get func(q *sqlcgen.Queries) (*sqlcgen.Address, error)) (*entity.Address, error) {
	out, err := findRow(ctx, r.db, get, toAddress)
	if err != nil {
		return nil, fmt.Errorf("addresses: %s: %w", name, err)
	}

	return out, nil
}

// Get implements repo.Addresses.
func (r *Addresses) Get(ctx context.Context, id, userID int) (*entity.Address, error) {
	return r.one(ctx, "get", func(q *sqlcgen.Queries) (*sqlcgen.Address, error) {
		return q.GetUserAddress(ctx, sqlcgen.GetUserAddressParams{ID: id32(id), UserID: id32(userID)}) //nolint:wrapcheck // wrapped by one
	})
}

// GetDefault implements repo.Addresses.
func (r *Addresses) GetDefault(ctx context.Context, userID int) (*entity.Address, error) {
	return r.one(ctx, "get default", func(q *sqlcgen.Queries) (*sqlcgen.Address, error) {
		return q.GetDefaultAddress(ctx, id32(userID)) //nolint:wrapcheck // wrapped by one
	})
}

func emptyAddressChanges(c *repo.AddressChanges) bool {
	return !c.Name.Set && !c.Phone.Set && !c.ProvinceCode.Set && !c.CityCode.Set && !c.DistrictCode.Set &&
		!c.Province.Set && !c.City.Set && !c.District.Set && !c.Address.Set && !c.IsDefault.Set
}

// Update implements repo.Addresses.
func (r *Addresses) Update(ctx context.Context, id int, c *repo.AddressChanges) (*entity.Address, error) {
	if !emptyAddressChanges(c) {
		err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
			_, err := q.UpdateAddress(ctx, sqlcgen.UpdateAddressParams{
				SetName: c.Name.Set, Name: c.Name.Value, SetPhone: c.Phone.Set, Phone: c.Phone.Value,
				SetProvinceCode: c.ProvinceCode.Set, ProvinceCode: c.ProvinceCode.Value,
				SetCityCode: c.CityCode.Set, CityCode: c.CityCode.Value,
				SetDistrictCode: c.DistrictCode.Set, DistrictCode: c.DistrictCode.Value,
				SetProvince: c.Province.Set, Province: c.Province.Value, SetCity: c.City.Set, City: c.City.Value,
				SetDistrict: c.District.Set, District: c.District.Value, SetAddress: c.Address.Set, Address: c.Address.Value,
				SetIsDefault: c.IsDefault.Set, IsDefault: c.IsDefault.Value, ID: id32(id),
			})

			return err //nolint:wrapcheck // wrapped below
		})
		if err != nil {
			return nil, fmt.Errorf("addresses: update: %w", err)
		}
	}

	return r.one(ctx, "get", func(q *sqlcgen.Queries) (*sqlcgen.Address, error) {
		return q.GetActiveAddress(ctx, id32(id)) //nolint:wrapcheck // wrapped by one
	})
}

// MarkDefault implements repo.Addresses.
func (r *Addresses) MarkDefault(ctx context.Context, id, userID int) error {
	err := r.atomic(ctx, func(q *sqlcgen.Queries) error {
		if err := q.LockUserAddresses(ctx, id32(userID)); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		_, err := q.GetUserAddress(ctx, sqlcgen.GetUserAddressParams{ID: id32(id), UserID: id32(userID)})
		if notFound(err) {
			return nil
		}

		if err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		if err := q.ClearOtherDefaultAddresses(ctx, sqlcgen.ClearOtherDefaultAddressesParams{UserID: id32(userID), ID: id32(id)}); err != nil {
			return err //nolint:wrapcheck // wrapped below
		}

		return q.SetDefaultAddress(ctx, sqlcgen.SetDefaultAddressParams{ID: id32(id), UserID: id32(userID)}) //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return fmt.Errorf("addresses: mark default: %w", err)
	}

	return nil
}

// SoftDelete implements repo.Addresses.
func (r *Addresses) SoftDelete(ctx context.Context, id int) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.SoftDeleteAddress(ctx, id32(id)) })
	if err != nil {
		return fmt.Errorf("addresses: delete: %w", err)
	}

	return nil
}

// RemoveByUser implements repo.Addresses.
func (r *Addresses) RemoveByUser(ctx context.Context, userID int) error {
	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error { return q.DeleteUserAddresses(ctx, id32(userID)) })
	if err != nil {
		return fmt.Errorf("addresses: remove by user: %w", err)
	}

	return nil
}

// Regions implements repo.Addresses.
func (r *Addresses) Regions(ctx context.Context, codes []string) ([]entity.Region, error) {
	var out []entity.Region

	err := r.db.queries(ctx, func(q *sqlcgen.Queries) error {
		rows, err := q.GetAddressRegionsByCode(ctx, codes)
		for _, row := range rows {
			out = append(out, entity.Region{Code: row.Code, Name: row.Name, Level: int(row.Level), ParentCode: row.ParentCode})
		}

		return err //nolint:wrapcheck // wrapped below
	})
	if err != nil {
		return nil, fmt.Errorf("addresses: regions: %w", err)
	}

	return out, nil
}
