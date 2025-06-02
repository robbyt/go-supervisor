package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
)

// Route represents a single HTTP route with a name, path, and handler chain.
type Route struct {
	name     string // internal identifier for the route, used for equality checks
	Path     string
	Handlers []HandlerFunc
}

// newRoute is a private/internal constructor used by the other route creation functions.
func newRoute(name, path string, handlers ...HandlerFunc) (*Route, error) {
	if name == "" {
		return nil, errors.New("name cannot be empty")
	}
	if path == "" {
		return nil, errors.New("path cannot be empty")
	}
	if len(handlers) == 0 {
		return nil, errors.New("at least one handler required")
	}

	return &Route{
		name:     name,
		Path:     path,
		Handlers: handlers,
	}, nil
}

// NewRouteFromHandlerFunc creates a new Route with the given name, path, and handler. Optionally, it can include middleware functions.
// This is the preferred way to create routes in the httpserver package.
func NewRouteFromHandlerFunc(
	name string,
	path string,
	handler http.HandlerFunc,
	middlewares ...HandlerFunc,
) (*Route, error) {
	if handler == nil {
		return nil, errors.New("handler cannot be nil")
	}
	h := func(rp *RequestProcessor) {
		handler.ServeHTTP(rp.Writer(), rp.Request())
	}
	middlewares = append(middlewares, h)
	return newRoute(name, path, middlewares...)
}

// NewRoute creates a new Route with the given name, path, and handler.
// Deprecated: Use NewRouteFromHandlerFunc instead.
func NewRoute(name string, path string, handler http.HandlerFunc) (*Route, error) {
	return NewRouteFromHandlerFunc(name, path, handler)
}

// NewRouteWithMiddleware creates a new Route with the given name, path, and handler, along with optional middleware.
// Deprecated: Use NewRouteFromHandlerFunc instead.
func NewRouteWithMiddleware(
	name string,
	path string,
	handler http.HandlerFunc,
	middlewares ...HandlerFunc,
) (*Route, error) {
	return NewRouteFromHandlerFunc(name, path, handler, middlewares...)
}

// ServeHTTP adapts the route to work with standard http.Handler
func (r *Route) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rp := &RequestProcessor{
		writer:   newResponseWriter(w),
		request:  req,
		handlers: r.Handlers,
		index:    -1,
	}

	rp.Next()
}

// NewWildcardRoute creates a route that handles everything under /prefix/*.
// Clients calling /prefix/foo/bar will match this route.
//
// Deprecated: Use NewRouteFromHandlerFunc with wildcard.New() middleware instead.
// This approach is more composable and follows the package's middleware pattern.
//
// Migration example:
//
//	// Old approach:
//	route, _ := httpserver.NewWildcardRoute("/api/", handler1, handler2)
//
//	// New approach:
//	import "github.com/robbyt/go-supervisor/runnables/httpserver/middleware/wildcard"
//	route, _ := httpserver.NewRouteFromHandlerFunc(
//	    "api",
//	    "/api/*",
//	    finalHandler,
//	    wildcard.New("/api/"),
//	    middleware1,
//	    middleware2,
//	)
func NewWildcardRoute(prefix string, handlers ...HandlerFunc) (*Route, error) {
	logger := slog.Default().WithGroup("httpserver.NewWildcardRoute")
	if prefix == "" {
		return nil, errors.New("prefix cannot be empty")
	}
	if len(handlers) == 0 {
		return nil, errors.New("at least one handler required")
	}
	for _, handler := range handlers {
		if handler == nil {
			return nil, errors.New("handler cannot be nil")
		}
	}

	if !strings.HasPrefix(prefix, "/") {
		logger.Warn("Prepending slash to prefix", "oldPrefix", prefix, "newPrefix", "/"+prefix)
		prefix = "/" + prefix
	}

	if !strings.HasSuffix(prefix, "/") {
		logger.Debug("Appending slash to prefix", "oldPrefix", prefix, "newPrefix", prefix+"/")
		prefix = prefix + "/"
	}

	// Create wildcard handler that strips prefix
	wildcardHandler := func(rp *RequestProcessor) {
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

	// Prepend wildcard handler to the handlers
	allHandlers := append([]HandlerFunc{wildcardHandler}, handlers...)

	return newRoute(
		fmt.Sprintf("wildcard:%s", prefix),
		prefix,
		allHandlers...,
	)
}

func (r Route) Equal(other Route) bool {
	if r.Path != other.Path {
		return false
	}

	if r.name != other.name {
		return false
	}

	return true
}

// Routes is a map of paths as strings, that route to http.HandlerFuncs
type Routes []Route

// Equal compares two routes and returns true if they are equal, false otherwise.
// This works because we assume the route names uniquely identify the route.
// For example, the route name could be based on a content hash or other unique identifier.
func (r Routes) Equal(other Routes) bool {
	// First compare the lengths of both routes, if they are different they can't be equal
	if len(r) != len(other) {
		return false
	}

	// now compare the names of both routes, if they are different, they are not equal
	oldNames := make([]string, 0, len(r))
	for _, route := range r {
		oldNames = append(oldNames, route.name)
	}
	slices.Sort(oldNames)

	newNames := make([]string, 0, len(other))
	for _, route := range other {
		newNames = append(newNames, route.name)
	}
	slices.Sort(newNames)

	if fmt.Sprintf("%v", oldNames) != fmt.Sprintf("%v", newNames) {
		return false
	}

	// now compare the paths of both routes, if they are different they are not equal
	routeMap := make(map[string]Route)
	for _, route := range r {
		routeMap[route.Path] = route
	}

	for _, otherRoute := range other {
		route, exists := routeMap[otherRoute.Path]
		if !exists || !route.Equal(otherRoute) {
			return false
		}
	}

	return true
}

// String returns a string representation of all routes, including their versions and paths.
func (r Routes) String() string {
	if len(r) == 0 {
		return "Routes<>"
	}

	var routes []string
	for _, route := range r {
		routes = append(routes, fmt.Sprintf("Name: %s, Path: %s", route.name, route.Path))
	}
	slices.Sort(routes)

	return fmt.Sprintf("Routes<%s>", strings.Join(routes, ", "))
}
