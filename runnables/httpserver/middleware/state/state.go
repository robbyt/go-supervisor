package state

import "github.com/robbyt/go-supervisor/runnables/httpserver"

// New creates a middleware that adds the current server state
// to the response headers.
func New(stateProvider func() string) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		// Add state header if we have a state provider
		if stateProvider != nil {
			state := stateProvider()
			rp.Writer().Header().Set("X-Server-State", state)
		}

		rp.Next()
	}
}
