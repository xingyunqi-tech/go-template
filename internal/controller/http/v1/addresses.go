package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/reqctx"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/repo"
	"github.com/xingyunqi-tech/go-template/internal/usecase/address"
	"github.com/xingyunqi-tech/go-template/pkg/pagination"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

const (
	_defaultPageLimit = 10
	_maxPageLimit     = 50
)

// callerID is the id of the authenticated user.
func callerID(ctx context.Context) int {
	return reqctx.Principal(ctx).UserID
}

// queryFloat is a numeric query value, or def when it is absent.
func queryFloat(o *validatorx.Object, key string, def float64) float64 {
	if v, ok := field(o, key); ok {
		if f, isNumber := v.(float64); isNumber {
			return f
		}
	}

	return def
}

// pageAndLimit reads page (default 1) and limit (default 10, at most 50).
func pageAndLimit(q *validatorx.Object) (page, limit float64) {
	return queryFloat(q, "page", 1), min(queryFloat(q, "limit", _defaultPageLimit), _maxPageLimit)
}

func optBool(o *validatorx.Object, key string) repo.Optional[bool] {
	v, ok := field(o, key)

	switch {
	case !ok:
		return repo.Optional[bool]{Set: false, Value: nil}
	case v == nil:
		return repo.Null[bool]()
	}

	b, _ := v.(bool)

	return repo.Some(b)
}

func truthyString(o *validatorx.Object, key string) string {
	if o == nil {
		return ""
	}

	v, ok := field(o, key)
	if !ok || !truthy(v) {
		return ""
	}

	return validatorx.ToString(v)
}

func addressInput(o *validatorx.Object) *address.Input {
	return &address.Input{
		Name: optString(o, "name"), Phone: optString(o, "phone"), ProvinceCode: optString(o, "provinceCode"),
		CityCode: optString(o, "cityCode"), DistrictCode: optString(o, "districtCode"),
		Province: optString(o, "province"), City: optString(o, "city"), District: optString(o, "district"),
		Address: optString(o, "address"), IsDefault: optBool(o, "isDefault"),
	}
}

func addressList(ctx context.Context) *address.List {
	q := query(ctx)
	page, limit := pageAndLimit(q)
	filters := objectField(q, "filters")
	list := &address.List{
		UserID: callerID(ctx),
		Filter: repo.AddressFilter{
			Province: truthyString(filters, "province"), City: truthyString(filters, "city"),
			ProvinceCode: truthyString(filters, "provinceCode"), CityCode: truthyString(filters, "cityCode"),
			DistrictCode: truthyString(filters, "districtCode"),
		},
		Sort: nil, Page: page, Limit: limit,
	}
	positions := map[string]int{}

	for _, s := range objectItems(q, "sort") {
		orderBy, _ := s.Get("orderBy")
		order, _ := s.Get("order")
		sf := repo.SortField{Property: validatorx.ToString(orderBy), Descending: validatorx.ToString(order) == _descending}

		if i, seen := positions[sf.Property]; seen {
			list.Sort[i] = sf

			continue
		}

		positions[sf.Property] = len(list.Sort)
		list.Sort = append(list.Sort, sf)
	}

	return list
}

// GetAddresses handles GET addresses.
func (s *Server) GetAddresses(ctx context.Context, _ gen.GetAddressesRequestObject) (gen.GetAddressesResponseObject, error) {
	list := addressList(ctx)

	data, total, err := s.addresses.List(ctx, list)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	items := make([]view.Address, len(data))
	for i := range data {
		items[i] = view.NewAddress(&data[i])
	}

	return gen.GetAddresses200JSONResponse(pagination.New(items, float64(total), list.Page, list.Limit)), nil
}

// CreateAddress handles POST addresses.
func (s *Server) CreateAddress(ctx context.Context, _ gen.CreateAddressRequestObject) (gen.CreateAddressResponseObject, error) {
	a, err := s.addresses.Create(ctx, callerID(ctx), addressInput(body(ctx)))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.CreateAddress201JSONResponse(view.NewAddress(a)), nil
}

// GetDefaultAddress handles GET addresses/default.
func (s *Server) GetDefaultAddress(ctx context.Context, _ gen.GetDefaultAddressRequestObject) (gen.GetDefaultAddressResponseObject, error) {
	a, err := s.addresses.Default(ctx, callerID(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetDefaultAddress200JSONResponse(view.NewAddress(a)), nil
}

// GetAddress handles GET addresses/:id.
func (s *Server) GetAddress(ctx context.Context, req gen.GetAddressRequestObject) (gen.GetAddressResponseObject, error) {
	a, err := s.addresses.Get(ctx, int(req.Id), callerID(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetAddress200JSONResponse(view.NewAddress(a)), nil
}

// UpdateAddress handles PUT addresses/:id.
func (s *Server) UpdateAddress(ctx context.Context, req gen.UpdateAddressRequestObject) (gen.UpdateAddressResponseObject, error) {
	a, err := s.addresses.Update(ctx, int(req.Id), callerID(ctx), addressInput(body(ctx)))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.UpdateAddress200JSONResponse(view.NewAddress(a)), nil
}

// SetDefaultAddress handles PUT addresses/:id/default.
func (s *Server) SetDefaultAddress(ctx context.Context, req gen.SetDefaultAddressRequestObject) (gen.SetDefaultAddressResponseObject, error) {
	a, err := s.addresses.SetDefault(ctx, int(req.Id), callerID(ctx))
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.SetDefaultAddress200JSONResponse(view.NewAddress(a)), nil
}

// DeleteAddress handles DELETE addresses/:id.
func (s *Server) DeleteAddress(ctx context.Context, req gen.DeleteAddressRequestObject) (gen.DeleteAddressResponseObject, error) {
	if err := s.addresses.Remove(ctx, int(req.Id), callerID(ctx)); err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.DeleteAddress200Response{Body: nil}, nil
}
