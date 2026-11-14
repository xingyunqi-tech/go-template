package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/appprofile"
)

// GetAppProfile handles GET app-profile.
func (s *Server) GetAppProfile(ctx context.Context, _ gen.GetAppProfileRequestObject) (gen.GetAppProfileResponseObject, error) {
	p, err := s.appProfile.Get(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.GetAppProfile200JSONResponse(view.NewAppProfile(p)), nil
}

// AdminUpdateAppProfile handles PUT admin/app-profile.
func (s *Server) AdminUpdateAppProfile(ctx context.Context, _ gen.AdminUpdateAppProfileRequestObject) (gen.AdminUpdateAppProfileResponseObject, error) {
	b := body(ctx)

	p, err := s.appProfile.Update(ctx, appprofile.Profile{
		AppName: stringPtr(b, "appName"), Slogan: stringPtr(b, "slogan"), CompanyName: stringPtr(b, "companyName"),
		Copyright: stringPtr(b, "copyright"), IcpLicense: stringPtr(b, "icpLicense"),
		ServicePhone: stringPtr(b, "servicePhone"), ServiceHours: stringPtr(b, "serviceHours"),
		ServiceEmail: stringPtr(b, "serviceEmail"), ShareTitle: stringPtr(b, "shareTitle"),
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // use case errors are HTTP errors
	}

	return gen.AdminUpdateAppProfile200JSONResponse(view.NewAppProfile(p)), nil
}
