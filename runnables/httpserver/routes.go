package httpserver

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware"
)

// Route represents a single HTTP route with a name, path, and handler function.
type Route struct {
	name    string // internal identifier for the route, used for equality checks
	Path    string
	Handler http.HandlerFunc
}

// applyMiddlewares wraps a handler with multiple middleware functions.
// The first middleware in the list is the outermost wrapper (executed first).
func applyMiddlewares(handler http.HandlerFunc, middlewares ...middleware.Middleware) http.HandlerFunc {
	// Apply middlewares in reverse order so the first middleware is outermost
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// NewRoute creates a new Route with the given name, path, and handler.
// The name must be unique, the path must be non-empty, and the handler must not be nil.
func NewRoute(name string, path string, handler http.HandlerFunc) (*Route, error) {
	if name == "" {
		return nil, errors.New("name cannot be empty")
	}

	if path == "" {
		return nil, errors.New("path cannot be empty")
	}

	if handler == nil {
		return nil, errors.New("handler cannot be nil")
	}

	return &Route{
		name:    name,
		Path:    path,
		Handler: handler,
	}, nil
}

// NewRouteWithMiddleware creates a new Route with the given name, path, handler,
// and applies the provided middlewares to the handler in the order they are provided.
func NewRouteWithMiddleware(name string, path string, handler http.HandlerFunc, middlewares ...middleware.Middleware) (*Route, error) {
	if name == "" {
		return nil, errors.New("name cannot be empty")
	}

	if path == "" {
		return nil, errors.New("path cannot be empty")
	}

	if handler == nil {
		return nil, errors.New("handler cannot be nil")
	}

	wrappedHandler := applyMiddlewares(handler, middlewares...)

	return &Route{
		name:    name,
		Path:    path,
		Handler: wrappedHandler,
	}, nil
}

// NewWildcardRoute creates a route that handles everything under /prefix/*.
// Clients calling /prefix/foo/bar will match this route.
func NewWildcardRoute(prefix string, handler http.HandlerFunc, middlewares ...middleware.Middleware) (*Route, error) {
	logger := slog.Default().WithGroup("httpserver.NewWildcardRoute")
	if prefix == "" {
		return nil, errors.New("prefix cannot be empty")
	}
	if handler == nil {
		return nil, errors.New("handler cannot be nil")
	}

	if !strings.HasPrefix(prefix, "/") {
		logger.Warn("Prepending slash to prefix", "oldPrefix", prefix, "newPrefix", "/"+prefix)
		prefix = "/" + prefix
	}

	if !strings.HasSuffix(prefix, "/") {
		logger.Debug("Appending slash to prefix", "oldPrefix", prefix, "newPrefix", prefix+"/")
		prefix = prefix + "/"
	}

	// Create the wildcard middleware
	wildcardMiddleware := middleware.WildcardRouter(prefix)

	// Combine the wildcard middleware with any additional middlewares
	allMiddlewares := append([]middleware.Middleware{wildcardMiddleware}, middlewares...)

	return NewRouteWithMiddleware(
		fmt.Sprintf("wildcard:%s", prefix),
		prefix,
		handler,
		allMiddlewares...,
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
