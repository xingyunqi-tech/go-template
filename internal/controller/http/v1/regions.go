package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/entity"
	"github.com/xingyunqi-tech/go-template/internal/usecase/region"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// GetProvinces handles GET regions/provinces.
func (s *Server) GetProvinces(ctx context.Context, _ gen.GetProvincesRequestObject) (gen.GetProvincesResponseObject, error) {
	rs, err := s.regions.Provinces(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetProvinces200JSONResponse(view.NewRegions(rs)), nil
}

// GetCities handles GET regions/cities/:provinceCode.
func (s *Server) GetCities(ctx context.Context, req gen.GetCitiesRequestObject) (gen.GetCitiesResponseObject, error) {
	rs, err := s.regions.Cities(ctx, req.ProvinceCode)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetCities200JSONResponse(view.NewRegions(rs)), nil
}

// GetDistricts handles GET regions/districts/:cityCode.
func (s *Server) GetDistricts(ctx context.Context, req gen.GetDistrictsRequestObject) (gen.GetDistrictsResponseObject, error) {
	rs, err := s.regions.Districts(ctx, req.CityCode)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetDistricts200JSONResponse(view.NewRegions(rs)), nil
}

// GetRegionByCode handles GET regions/code/:code.
func (s *Server) GetRegionByCode(ctx context.Context, req gen.GetRegionByCodeRequestObject) (gen.GetRegionByCodeResponseObject, error) {
	r, err := s.regions.ByCode(ctx, req.Code)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	if r == nil {
		return nil, &entity.Error{
			Status: entity.StatusNotFound, Message: "Region with code " + req.Code + " not found",
			ErrorKey: "regionNotFound", Fields: nil,
		}
	}

	return gen.GetRegionByCode200JSONResponse(view.NewRegion(r)), nil
}

// GetRegionTree handles GET regions/tree.
func (s *Server) GetRegionTree(ctx context.Context, _ gen.GetRegionTreeRequestObject) (gen.GetRegionTreeResponseObject, error) {
	tree, err := s.regions.Tree(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetRegionTree200JSONResponse(view.NewRegionTree(tree)), nil
}

// QueryRegions handles GET regions.
func (s *Server) QueryRegions(ctx context.Context, _ gen.QueryRegionsRequestObject) (gen.QueryRegionsResponseObject, error) {
	q := query(ctx)
	in := region.Query{Level: number(q, "level", 0), ParentCode: nil, Keyword: ""}

	if v, ok := field(q, "parentCode"); ok && v != nil {
		parent := validatorx.ToString(v)
		in.ParentCode = &parent
	}

	if v, ok := field(q, "keyword"); ok && truthy(v) {
		in.Keyword = validatorx.ToString(v)
	}

	rs, err := s.regions.Query(ctx, in)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.QueryRegions200JSONResponse(view.NewRegions(rs)), nil
}
