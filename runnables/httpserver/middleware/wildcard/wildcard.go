package wildcard

import (
	"net/http"
	"strings"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that handles requests with a prefix pattern,
// stripping the prefix before passing to the handler.
//
// This middleware enables a single handler to manage multiple routes by removing
// a common prefix from the request path. The handler receives the path with the
// prefix stripped, making it easier to implement sub-routers or delegate handling
// to other routing systems.
//
// Why use this middleware:
//   - Route delegation: Pass "/api/*" requests to a separate API handler
//   - Legacy integration: Wrap existing handlers that expect different path structures
//   - Microservice routing: Forward requests to handlers that manage their own sub-paths
//   - File serving: Strip prefixes when serving static files from subdirectories
//
// Examples:
//
//	// API delegation - all /api/* requests go to apiHandler with prefix stripped
//	apiRoute, _ := httpserver.NewRouteFromHandlerFunc(
//	    "api",
//	    "/api/*",
//	    apiHandler,
//	    wildcard.New("/api/"),
//	)
//	// Request to "/api/users/123" becomes "/users/123" for apiHandler
//
//	// File serving - serve files from ./static/ directory
//	fileHandler := http.FileServer(http.Dir("./static/"))
//	staticRoute, _ := httpserver.NewRouteFromHandlerFunc(
//	    "static",
//	    "/static/*",
//	    fileHandler.ServeHTTP,
//	    wildcard.New("/static/"),
//	)
//	// Request to "/static/css/main.css" becomes "/css/main.css" for file server
//
//	// Legacy system integration - forward to old handler expecting different paths
//	legacyRoute, _ := httpserver.NewRouteFromHandlerFunc(
//	    "legacy",
//	    "/v1/*",
//	    legacySystemHandler,
//	    wildcard.New("/v1/"),
//	)
//	// Request to "/v1/old/endpoint" becomes "/old/endpoint" for legacy handler
//
// Behavior:
//   - Requests that don't match the prefix return 404 Not Found
//   - Exact prefix matches result in empty path being passed to handler
//   - Query parameters and request body are preserved unchanged
//   - All HTTP methods are supported
func New(prefix string) httpserver.HandlerFunc {
	// Input validation and normalization
	if prefix == "" {
		prefix = "/"
	}

	// Ensure prefix starts with /
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}

	// Ensure prefix ends with / (unless it's just "/")
	if prefix != "/" && !strings.HasSuffix(prefix, "/") {
		prefix = prefix + "/"
	}

	return func(rp *httpserver.RequestProcessor) {
		req := rp.Request()
		if !strings.HasPrefix(req.URL.Path, prefix) {
			http.NotFound(rp.Writer(), req)
			rp.Abort()
			return
		}
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		rp.Next()
	}
}
