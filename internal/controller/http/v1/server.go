// Package v1 implements gen.StrictServerInterface.
package v1

import "github.com/xingyunqi-tech/go-template/internal/controller/http/gen"

var _ gen.StrictServerInterface = (*Server)(nil)

// Server implements the generated strict server interface.
type Server struct {
	appName string
}

// New returns a Server.
func New(appName string) *Server {
	return &Server{appName: appName}
}
