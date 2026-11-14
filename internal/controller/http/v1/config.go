package v1

import (
	"context"
	"encoding/json"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/pkg/validatorx"
)

// GetConfig handles GET config/:key.
func (s *Server) GetConfig(ctx context.Context, req gen.GetConfigRequestObject) (gen.GetConfigResponseObject, error) {
	c, err := s.config.FindPublic(ctx, req.Key)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetConfig200JSONResponse(view.NewConfig(c)), nil
}

// GetConfigAsAdmin handles GET config/admin/:key.
func (s *Server) GetConfigAsAdmin(ctx context.Context, req gen.GetConfigAsAdminRequestObject) (gen.GetConfigAsAdminResponseObject, error) {
	c, err := s.config.Find(ctx, req.Key)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetConfigAsAdmin200JSONResponse(view.NewConfig(c)), nil
}

// UpsertConfig handles PUT config/:key.
func (s *Server) UpsertConfig(ctx context.Context, req gen.UpsertConfigRequestObject) (gen.UpsertConfigResponseObject, error) {
	b := body(ctx)

	v, _ := field(b, "value")
	value, _ := validatorx.Stringify(v)

	var isPublic *bool

	if p, ok := field(b, "isPublic"); ok {
		if flag, isBool := p.(bool); isBool {
			isPublic = &flag
		}
	}

	c, err := s.config.UpsertGeneric(ctx, req.Key, json.RawMessage(value), isPublic)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.UpsertConfig200JSONResponse(view.NewConfig(c)), nil
}
