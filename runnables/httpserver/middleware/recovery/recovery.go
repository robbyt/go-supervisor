package recovery

import (
	"log/slog"
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that recovers from panics in HTTP handlers.
// It returns a 500 Internal Server Error response when panics occur.
// If logger is provided, panics are logged. If logger is nil, recovery is silent.
func New(logger *slog.Logger) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		defer func() {
			if err := recover(); err != nil {
				req := rp.Request()
				writer := rp.Writer()

				if logger != nil {
					logger.Error("HTTP handler panic recovered",
						"error", err,
						"path", req.URL.Path,
						"method", req.Method,
					)
				}
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				rp.Abort()
			}
		}()

		rp.Next()
	}
}
