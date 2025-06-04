package recovery

import (
	"log/slog"
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that recovers from panics in HTTP handlers.
// It returns a 500 Internal Server Error response when panics occur.
// If handler is provided, panics are logged. If handler is nil, recovery is silent.
func New(handler slog.Handler) httpserver.HandlerFunc {
	// Default logger that does nothing if no handler is provided
	// This allows the middleware to be used without logging if passed a nil handler.
	logger := func(err any, path string, method string) {}

	if handler != nil {
		logger = func(err any, path string, method string) {
			slogger := slog.New(handler)
			slogger.Error("HTTP handler panic recovered",
				"error", err,
				"path", path,
				"method", method,
			)
		}
	}

	return func(rp *httpserver.RequestProcessor) {
		defer func() {
			if err := recover(); err != nil {
				req := rp.Request()
				writer := rp.Writer()
				logger(err, req.URL.Path, req.Method)
				http.Error(writer, "Internal Server Error", http.StatusInternalServerError)
				rp.Abort()
			}
		}()

		rp.Next()
	}
}
