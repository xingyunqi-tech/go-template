// Package address manages shipping addresses like the nestjs AddressesService: region code
// validation with name filling, a per-user limit and a single default address.
package address

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
)

const (
	// MaxPerUser is the number of active addresses a user may keep.
	MaxPerUser = 20

	_cleanerOrder = 110
	_codeCount    = 3
)

// Input is a create or update request; unset fields are left alone.
type Input struct {
	Name         repo.Optional[string]
	Phone        repo.Optional[string]
	ProvinceCode repo.Optional[string]
	CityCode     repo.Optional[string]
	DistrictCode repo.Optional[string]
	Province     repo.Optional[string]
	City         repo.Optional[string]
	District     repo.Optional[string]
	Address      repo.Optional[string]
	IsDefault    repo.Optional[bool]
}

// List selects a page of the user's addresses; page and limit are already defaulted and capped.
type List struct {
	UserID int
	Filter repo.AddressFilter
	Sort   []repo.SortField
	Page   float64
	Limit  float64
}

// Service manages addresses.
type Service struct {
	addresses repo.Addresses
	log       *slog.Logger
}

// New returns a Service.
func New(addresses repo.Addresses, log *slog.Logger) *Service {
	return &Service{addresses: addresses, log: log}
}

type regionData struct {
	codes [_codeCount]*string
	names [_codeCount]string
}

func badRequest(key, message string) *entity.Error {
	return &entity.Error{Status: entity.StatusBadRequest, Message: message, ErrorKey: key, Fields: nil}
}

func notFound() *entity.Error {
	return entity.NotFoundKey("addressNotFound")
}

func given(s *string) bool {
	return s != nil && *s != ""
}

// region checks that the three codes come together and fills the names from them; without codes
// the names are required.
func (s *Service) region(ctx context.Context, anyCode bool, codes, names [_codeCount]*string) (*regionData, error) {
	hasAll := given(codes[0]) && given(codes[1]) && given(codes[2])

	if anyCode && !hasAll {
		s.log.WarnContext(ctx, "incomplete region codes")

		return nil, badRequest("incompleteRegionCodes", "Must provide all three region codes (province, city, district) or none at all")
	}

	if !hasAll {
		if !given(names[0]) || !given(names[1]) || !given(names[2]) {
			return nil, badRequest("missingRegionNames",
				"Province, city, and district names are required when region codes are not provided")
		}

		return &regionData{codes: [_codeCount]*string{}, names: [_codeCount]string{*names[0], *names[1], *names[2]}}, nil
	}

	return s.combination(ctx, [_codeCount]string{*codes[0], *codes[1], *codes[2]})
}

// combination checks that the codes are a province, one of its cities and one of that city's
// districts, and returns them with the region names.
func (s *Service) combination(ctx context.Context, codes [_codeCount]string) (*regionData, error) {
	regions, err := s.addresses.Regions(ctx, codes[:])
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	byCode := make(map[string]entity.Region, len(regions))
	for _, r := range regions {
		byCode[r.Code] = r
	}

	levels := [_codeCount]int{entity.RegionProvince, entity.RegionCity, entity.RegionDistrict}
	out := &regionData{codes: [_codeCount]*string{}, names: [_codeCount]string{}}
	parent := ""

	for i, code := range codes {
		r, ok := byCode[code]
		if !ok || r.Level != levels[i] || (i > 0 && (r.ParentCode == nil || *r.ParentCode != parent)) {
			s.log.WarnContext(ctx, "invalid region combination", "province_code", codes[0], "city_code", codes[1], "district_code", codes[2])

			return nil, badRequest("invalidRegionCombination", "Invalid province/city/district combination")
		}

		out.codes[i] = &codes[i]
		out.names[i] = r.Name
		parent = code
	}

	return out, nil
}

// Create adds an address; the first address, or one sent with isDefault, becomes the default.
func (s *Service) Create(ctx context.Context, userID int, in *Input) (*entity.Address, error) {
	anyCode := in.ProvinceCode.Set || in.CityCode.Set || in.DistrictCode.Set

	region, err := s.region(ctx, anyCode,
		[_codeCount]*string{in.ProvinceCode.Value, in.CityCode.Value, in.DistrictCode.Value},
		[_codeCount]*string{in.Province.Value, in.City.Value, in.District.Value})
	if err != nil {
		return nil, err
	}

	created, previous, err := s.addresses.CreateWithinLimit(ctx, &repo.NewAddress{
		UserID: userID, Name: deref(in.Name.Value), Phone: deref(in.Phone.Value),
		ProvinceCode: region.codes[0], CityCode: region.codes[1], DistrictCode: region.codes[2],
		Province: region.names[0], City: region.names[1], District: region.names[2], Address: deref(in.Address.Value),
	}, MaxPerUser)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if created == nil {
		s.log.WarnContext(ctx, "max addresses limit reached", "user_id", userID)

		return nil, &entity.Error{
			Status: entity.StatusForbidden, Message: fmt.Sprintf("Maximum %d addresses allowed per user", MaxPerUser),
			ErrorKey: "maxAddressesReached", Fields: nil,
		}
	}

	makeDefault := previous == 0
	if in.IsDefault.Value != nil {
		makeDefault = *in.IsDefault.Value
	}

	if makeDefault {
		if err := s.addresses.MarkDefault(ctx, created.ID, userID); err != nil {
			return nil, err //nolint:wrapcheck // repo errors are wrapped
		}

		created.IsDefault = true
	}

	return created, nil
}

// List returns a page of the user's addresses and their total.
func (s *Service) List(ctx context.Context, q *List) ([]entity.Address, int, error) {
	offset, limit := pagination.OffsetLimit(q.Page, q.Limit)

	data, err := s.addresses.List(ctx, &repo.AddressList{UserID: q.UserID, Filter: q.Filter, Sort: q.Sort, Offset: offset, Limit: limit})
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	total, err := s.addresses.Count(ctx, q.UserID, &q.Filter)
	if err != nil {
		return nil, 0, err //nolint:wrapcheck // repo errors are wrapped
	}

	return data, total, nil
}

// Get returns an address of the user or a 404 addressNotFound.
func (s *Service) Get(ctx context.Context, id, userID int) (*entity.Address, error) {
	a, err := s.addresses.Get(ctx, id, userID)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if a == nil {
		return nil, notFound()
	}

	return a, nil
}

// Default returns the user's default address, or nil.
func (s *Service) Default(ctx context.Context, userID int) (*entity.Address, error) {
	return s.addresses.GetDefault(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}

func deref(p *string) string {
	if p == nil {
		return ""
	}

	return *p
}

func orCurrent(v, current *string) *string {
	if v != nil {
		return v
	}

	return current
}

// changes is the update payload: every sent field but isDefault, which is only written when false;
// sent region codes are validated together with the stored ones.
func (s *Service) changes(ctx context.Context, current *entity.Address, in *Input) (*repo.AddressChanges, error) {
	c := &repo.AddressChanges{
		Name: in.Name, Phone: in.Phone, ProvinceCode: in.ProvinceCode, CityCode: in.CityCode,
		DistrictCode: in.DistrictCode, Province: in.Province, City: in.City, District: in.District,
		Address: in.Address, IsDefault: repo.Optional[bool]{Set: false, Value: nil},
	}

	if in.IsDefault.Value != nil && !*in.IsDefault.Value {
		c.IsDefault = repo.Some(false)
	}

	if !in.ProvinceCode.Set && !in.CityCode.Set && !in.DistrictCode.Set {
		return c, nil
	}

	codes := [_codeCount]*string{
		orCurrent(in.ProvinceCode.Value, current.ProvinceCode),
		orCurrent(in.CityCode.Value, current.CityCode),
		orCurrent(in.DistrictCode.Value, current.DistrictCode),
	}
	names := [_codeCount]*string{
		orCurrent(in.Province.Value, &current.Province),
		orCurrent(in.City.Value, &current.City),
		orCurrent(in.District.Value, &current.District),
	}

	region, err := s.region(ctx, codes[0] != nil || codes[1] != nil || codes[2] != nil, codes, names)
	if err != nil {
		return nil, err
	}

	c.ProvinceCode = repo.Optional[string]{Set: true, Value: region.codes[0]}
	c.CityCode = repo.Optional[string]{Set: true, Value: region.codes[1]}
	c.DistrictCode = repo.Optional[string]{Set: true, Value: region.codes[2]}
	c.Province = repo.Some(region.names[0])
	c.City = repo.Some(region.names[1])
	c.District = repo.Some(region.names[2])

	return c, nil
}

// Update changes an address of the user; isDefault true makes it the only default address.
func (s *Service) Update(ctx context.Context, id, userID int, in *Input) (*entity.Address, error) {
	current, err := s.Get(ctx, id, userID)
	if err != nil {
		return nil, err
	}

	c, err := s.changes(ctx, current, in)
	if err != nil {
		return nil, err
	}

	updated, err := s.addresses.Update(ctx, id, c)
	if err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	if updated == nil {
		return nil, notFound()
	}

	if in.IsDefault.Value != nil && *in.IsDefault.Value {
		if err := s.addresses.MarkDefault(ctx, id, userID); err != nil {
			return nil, err //nolint:wrapcheck // repo errors are wrapped
		}

		updated.IsDefault = true
	}

	return updated, nil
}

// SetDefault makes an address of the user the only default one.
func (s *Service) SetDefault(ctx context.Context, id, userID int) (*entity.Address, error) {
	if _, err := s.Get(ctx, id, userID); err != nil {
		return nil, err
	}

	if err := s.addresses.MarkDefault(ctx, id, userID); err != nil {
		return nil, err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.Get(ctx, id, userID)
}

// Remove soft-deletes an address of the user; when it was the default, the first remaining
// address in list order becomes the default.
func (s *Service) Remove(ctx context.Context, id, userID int) error {
	a, err := s.Get(ctx, id, userID)
	if err != nil {
		return err
	}

	if err := s.addresses.SoftDelete(ctx, id); err != nil {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	if !a.IsDefault {
		return nil
	}

	rest, err := s.addresses.List(ctx, &repo.AddressList{UserID: userID, Filter: repo.AddressFilter{}, Sort: nil, Offset: 0, Limit: 1})
	if err != nil || len(rest) == 0 {
		return err //nolint:wrapcheck // repo errors are wrapped
	}

	return s.addresses.MarkDefault(ctx, rest[0].ID, userID) //nolint:wrapcheck // repo errors are wrapped
}

// Cleaner deletes the addresses of a removed account.
type Cleaner struct {
	addresses repo.Addresses
}

// NewCleaner returns the addresses account cleaner (order 110).
func NewCleaner(addresses repo.Addresses) *Cleaner {
	return &Cleaner{addresses: addresses}
}

// Name implements accountcleanup.Cleaner.
func (*Cleaner) Name() string { return "addresses" }

// Order implements accountcleanup.Cleaner.
func (*Cleaner) Order() int { return _cleanerOrder }

// Clean implements accountcleanup.Cleaner.
func (c *Cleaner) Clean(ctx context.Context, userID int) error {
	return c.addresses.RemoveByUser(ctx, userID) //nolint:wrapcheck // repo errors are wrapped
}
