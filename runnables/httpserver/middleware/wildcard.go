package middleware

import (
	"net/http"
	"strings"
)

// WildcardRouter creates a middleware that handles requests with a prefix pattern,
// stripping the prefix before passing to the handler, useful for passing a group
// of routes to a single handler function.
func WildcardRouter(prefix string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, prefix) {
				http.NotFound(w, r)
				return
			}
			http.StripPrefix(prefix, next).ServeHTTP(w, r)
		}
	}
}
