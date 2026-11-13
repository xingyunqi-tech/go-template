// Package v1 implements gen.StrictServerInterface.
package v1

import (
	"time"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/gen"
)

var _ gen.StrictServerInterface = (*Server)(nil)

// Server implements the generated strict server interface.
type Server struct {
	appName string
	started time.Time
	now     func() time.Time
}

// New returns a Server; uptime counts from this call.
func New(appName string) *Server {
	return &Server{appName: appName, started: time.Now(), now: time.Now}
}
