package wildcard

import (
	"net/http"
	"strings"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that handles requests with a prefix pattern,
// stripping the prefix before passing to the handler, useful for passing a group
// of routes to a single handler function.
func New(prefix string) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		req := rp.Request()
		if !strings.HasPrefix(req.URL.Path, prefix) {
			http.NotFound(rp.Writer(), req)
			rp.Abort()
			return
		}
		// Strip the prefix from the path
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		rp.Next()
	}
}
