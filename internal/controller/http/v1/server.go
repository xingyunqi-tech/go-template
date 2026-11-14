// Package v1 implements gen.StrictServerInterface.
package v1

import (
	"log/slog"
	"time"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
	"github.com/xingyunqi-tech/go-template/internal/controller/http/v1/view"
	"github.com/xingyunqi-tech/go-template/internal/usecase/auth"
	"github.com/xingyunqi-tech/go-template/internal/usecase/file"
	"github.com/xingyunqi-tech/go-template/internal/usecase/health"
	"github.com/xingyunqi-tech/go-template/internal/usecase/user"
)

var _ gen.StrictServerInterface = (*Server)(nil)

// Server implements the generated strict server interface.
type Server struct {
	appName string
	started time.Time
	now     func() time.Time
	auth    *auth.Service
	users   *user.Service
	urls    view.FileURLs
	health  *health.Service
	log     *slog.Logger
	files   *file.Service
	// presigned is the s3-presigned upload driver.
	presigned bool
}

// Deps are the Server dependencies.
type Deps struct {
	AppName string
	Auth    *auth.Service
	Users   *user.Service
	URLs    view.FileURLs
	Health  *health.Service
	Log     *slog.Logger
	Files   *file.Service
	// PresignedUploads selects the s3-presigned upload endpoint.
	PresignedUploads bool
}

// New returns a Server; uptime counts from this call.
func New(d *Deps) *Server {
	return &Server{
		appName: d.AppName, started: time.Now(), now: time.Now, auth: d.Auth, users: d.Users, urls: d.URLs,
		health: d.Health, log: d.Log, files: d.Files, presigned: d.PresignedUploads,
	}
}
