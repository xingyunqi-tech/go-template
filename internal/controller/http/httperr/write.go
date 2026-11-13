package httperr

import (
	"net/http"

	"github.com/xingyunqi-tech/go-template/internal/controller/http/response"
)

// Write sends body with its extra headers.
func Write(w http.ResponseWriter, body Body) error {
	for name, values := range body.Headers {
		for _, v := range values {
			w.Header().Add(name, v)
		}
	}

	var data any
	if body.Data != nil {
		data = body.Data
	}

	return response.WriteError(w, body.Status, body.Message, data)
}
