package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

const (
	_scalarVersion = "1.76.0"
	_docsCSP       = "default-src 'self';script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net;" +
		"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net;img-src 'self' data: https:;" +
		"font-src 'self' https: data:;connect-src 'self';object-src 'none';frame-ancestors 'self'"
	_docsHTML = `<!doctype html>
<html>
  <head>
    <title>API</title>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
  </head>
  <body>
    <script id="api-reference" data-url="/docs-json"></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference@` + _scalarVersion + `"></script>
  </body>
</html>
`
)

func mountDocs(r chi.Router, baseline []byte) {
	r.Get("/docs-json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(baseline)
	})
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", _docsCSP)
		_, _ = w.Write([]byte(_docsHTML))
	})
}
