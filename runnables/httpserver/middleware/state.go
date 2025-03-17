package middleware

import "net/http"

// StateDebugger creates a middleware that adds the current server state
// to the response headers.
func StateDebugger(stateProvider func() string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Add state header if we have a state provider
			if stateProvider != nil {
				state := stateProvider()
				w.Header().Set("X-Server-State", state)
			}

			next(w, r)
		}
	}
}
