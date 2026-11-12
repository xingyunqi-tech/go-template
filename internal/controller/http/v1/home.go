package v1

import (
	"context"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
)

// GetAppInfo handles GET /.
func (s *Server) GetAppInfo(context.Context, gen.GetAppInfoRequestObject) (gen.GetAppInfoResponseObject, error) {
	return gen.GetAppInfo200Response{Body: view.AppInfo{Name: s.appName}}, nil
}
