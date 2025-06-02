package recovery

import (
	"log/slog"
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that recovers from panics in HTTP handlers.
// It logs the error and returns a 500 Internal Server Error response.
func New(logger *slog.Logger) httpserver.HandlerFunc {
	if logger == nil {
		logger = slog.Default().WithGroup("httpserver")
	}

	return func(rp *httpserver.RequestProcessor) {
		defer func() {
			if err := recover(); err != nil {
				req := rp.Request()
				writer := rp.Writer()

				logger.Error("HTTP handler panic recovered",
					"error", err,
					"path", req.URL.Path,
					"method", req.Method,
				)
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				rp.Abort()
			}
		}()

		rp.Next()
	}
}
