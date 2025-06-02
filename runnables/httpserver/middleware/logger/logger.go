package logger

import (
	"log/slog"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that logs information about HTTP requests.
// It logs the request method, path, status code, and response time.
func New(logger *slog.Logger) httpserver.HandlerFunc {
	if logger == nil {
		logger = slog.Default().WithGroup("httpserver")
	}

	return func(rp *httpserver.RequestProcessor) {
		start := time.Now()

		// Process request
		rp.Next()

		// Log after request is processed
		duration := time.Since(start)
		req := rp.Request()
		writer := rp.Writer()

		logger.Info("HTTP request",
			"method", req.Method,
			"path", req.URL.Path,
			"status", writer.Status(),
			"duration", duration,
			"size", writer.Size(),
			"user_agent", req.UserAgent(),
			"remote_addr", req.RemoteAddr,
		)
	}
}
