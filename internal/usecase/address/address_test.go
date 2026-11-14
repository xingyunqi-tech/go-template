package address_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/address"
)

type fakeAddresses struct {
	repo.Addresses

	rows    []*entity.Address
	regions []entity.Region
	marked  []int
}

func (f *fakeAddresses) CreateWithinLimit(_ context.Context, a *repo.NewAddress, limit int) (*entity.Address, int, error) {
	if len(f.rows) >= limit {
		return nil, len(f.rows), nil
	}

	row := &entity.Address{
		ID: len(f.rows) + 1, UserID: a.UserID, Name: a.Name, ProvinceCode: a.ProvinceCode, CityCode: a.CityCode,
		DistrictCode: a.DistrictCode, Province: a.Province, City: a.City, District: a.District,
	}
	f.rows = append(f.rows, row)

	return row, len(f.rows) - 1, nil
}

func (f *fakeAddresses) Get(_ context.Context, id, _ int) (*entity.Address, error) {
	for _, r := range f.rows {
		if r.ID == id {
			c := *r

			return &c, nil
		}
	}

	return nil, nil //nolint:nilnil // not found
}

func (f *fakeAddresses) MarkDefault(_ context.Context, id, _ int) error {
	f.marked = append(f.marked, id)

	for _, r := range f.rows {
		r.IsDefault = r.ID == id
	}

	return nil
}

func (f *fakeAddresses) Regions(_ context.Context, codes []string) ([]entity.Region, error) {
	var out []entity.Region

	for _, r := range f.regions {
		for _, c := range codes {
			if r.Code == c {
				out = append(out, r)

				break
			}
		}
	}

	return out, nil
}

func (f *fakeAddresses) Update(ctx context.Context, id int, c *repo.AddressChanges) (*entity.Address, error) {
	for _, r := range f.rows {
		if r.ID == id && c.Province.Value != nil {
			r.Province = *c.Province.Value
			r.ProvinceCode = c.ProvinceCode.Value
		}
	}

	return f.Get(ctx, id, 0)
}

func (f *fakeAddresses) SoftDelete(_ context.Context, id int) error {
	for i, r := range f.rows {
		if r.ID == id {
			f.rows = append(f.rows[:i], f.rows[i+1:]...)

			break
		}
	}

	return nil
}

func (f *fakeAddresses) List(_ context.Context, q *repo.AddressList) ([]entity.Address, error) {
	var out []entity.Address

	for _, r := range f.rows {
		if int64(len(out)) < q.Limit {
			out = append(out, *r)
		}
	}

	return out, nil
}

func ptr(s string) *string { return &s }

func regions() []entity.Region {
	return []entity.Region{
		{Code: "11", Name: "Beijing", Level: entity.RegionProvince, ParentCode: nil},
		{Code: "1101", Name: "City", Level: entity.RegionCity, ParentCode: ptr("11")},
		{Code: "110101", Name: "Dongcheng", Level: entity.RegionDistrict, ParentCode: ptr("1101")},
		{Code: "1201", Name: "Tianjin", Level: entity.RegionCity, ParentCode: ptr("12")},
	}
}

func newService() (*address.Service, *fakeAddresses) {
	f := &fakeAddresses{Addresses: nil, rows: nil, regions: regions(), marked: nil}

	return address.New(f, slog.New(slog.DiscardHandler)), f
}

func input(fields map[string]*string) *address.Input {
	in := &address.Input{}
	targets := map[string]*repo.Optional[string]{
		"provinceCode": &in.ProvinceCode, "cityCode": &in.CityCode, "districtCode": &in.DistrictCode,
		"province": &in.Province, "city": &in.City, "district": &in.District,
	}

	for k, v := range fields {
		*targets[k] = repo.Optional[string]{Set: true, Value: v}
	}

	in.Name = repo.Some("N")

	return in
}

func errorKey(t *testing.T, err error) string {
	t.Helper()

	var e *entity.Error
	require.ErrorAs(t, err, &e)

	return e.ErrorKey
}

func TestCreateValidatesRegionCodes(t *testing.T) {
	t.Parallel()

	names := map[string]*string{"province": ptr("P"), "city": ptr("C"), "district": ptr("D")}
	tests := []struct {
		name   string
		fields map[string]*string
		want   string
	}{
		{"one code", map[string]*string{"provinceCode": ptr("11"), "province": ptr("P"), "city": ptr("C"), "district": ptr("D")}, "incompleteRegionCodes"},
		{"null code", map[string]*string{"provinceCode": nil, "province": ptr("P"), "city": ptr("C"), "district": ptr("D")}, "incompleteRegionCodes"},
		{"missing names", map[string]*string{"province": ptr("P")}, "missingRegionNames"},
		{"wrong parent", map[string]*string{"provinceCode": ptr("11"), "cityCode": ptr("1201"), "districtCode": ptr("110101")}, "invalidRegionCombination"},
		{"wrong level", map[string]*string{"provinceCode": ptr("1101"), "cityCode": ptr("1101"), "districtCode": ptr("110101")}, "invalidRegionCombination"},
		{"unknown code", map[string]*string{"provinceCode": ptr("11"), "cityCode": ptr("1101"), "districtCode": ptr("119999")}, "invalidRegionCombination"},
		{"names only", names, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, _ := newService()

			_, err := s.Create(t.Context(), 1, input(tt.fields))
			if tt.want == "" {
				require.NoError(t, err)

				return
			}

			assert.Equal(t, tt.want, errorKey(t, err))
		})
	}
}

func TestCreateFillsNamesAndDefaults(t *testing.T) {
	t.Parallel()

	s, f := newService()

	first, err := s.Create(t.Context(), 1, input(map[string]*string{
		"provinceCode": ptr("11"), "cityCode": ptr("1101"), "districtCode": ptr("110101"), "province": ptr("ignored"),
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{"Beijing", "City", "Dongcheng"}, []string{first.Province, first.City, first.District})
	assert.True(t, first.IsDefault)

	second, err := s.Create(t.Context(), 1, input(map[string]*string{"province": ptr("P"), "city": ptr("C"), "district": ptr("D")}))
	require.NoError(t, err)
	assert.False(t, second.IsDefault)

	explicit := input(map[string]*string{"province": ptr("P"), "city": ptr("C"), "district": ptr("D")})
	explicit.IsDefault = repo.Some(true)

	third, err := s.Create(t.Context(), 1, explicit)
	require.NoError(t, err)
	assert.True(t, third.IsDefault)
	assert.Equal(t, []int{1, 3}, f.marked)

	for range address.MaxPerUser - len(f.rows) {
		_, err := s.Create(t.Context(), 1, input(map[string]*string{"province": ptr("P"), "city": ptr("C"), "district": ptr("D")}))
		require.NoError(t, err)
	}

	_, err = s.Create(t.Context(), 1, input(map[string]*string{"province": ptr("P"), "city": ptr("C"), "district": ptr("D")}))
	assert.Equal(t, "maxAddressesReached", errorKey(t, err))
}

func TestUpdateMergesStoredRegionCodes(t *testing.T) {
	t.Parallel()

	s, _ := newService()

	plain, err := s.Create(t.Context(), 1, input(map[string]*string{"province": ptr("P"), "city": ptr("C"), "district": ptr("D")}))
	require.NoError(t, err)

	_, err = s.Update(t.Context(), plain.ID, 1, input(map[string]*string{"cityCode": ptr("1101")}))
	assert.Equal(t, "incompleteRegionCodes", errorKey(t, err))

	updated, err := s.Update(t.Context(), plain.ID, 1, input(map[string]*string{"provinceCode": nil}))
	require.NoError(t, err)
	assert.Equal(t, "P", updated.Province)

	_, err = s.Update(t.Context(), 99, 1, input(nil))
	assert.Equal(t, "addressNotFound", errorKey(t, err))
}

func TestRemoveDefaultPromotesNext(t *testing.T) {
	t.Parallel()

	s, f := newService()
	names := map[string]*string{"province": ptr("P"), "city": ptr("C"), "district": ptr("D")}

	first, err := s.Create(t.Context(), 1, input(names))
	require.NoError(t, err)

	second, err := s.Create(t.Context(), 1, input(names))
	require.NoError(t, err)

	require.NoError(t, s.Remove(t.Context(), second.ID, 1))
	assert.Equal(t, []int{first.ID}, f.marked)

	require.NoError(t, s.Remove(t.Context(), first.ID, 1))
	assert.Equal(t, []int{first.ID}, f.marked)
	assert.Equal(t, "addressNotFound", errorKey(t, s.Remove(t.Context(), first.ID, 1)))
}
