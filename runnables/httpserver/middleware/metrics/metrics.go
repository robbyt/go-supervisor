package metrics

import (
	"log/slog"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that collects metrics about HTTP requests.
// This is a placeholder implementation that can be extended to integrate with
// your metrics collection system.
func New() httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		start := time.Now()

		// Process request
		rp.Next()

		// Record metrics
		duration := time.Since(start)
		req := rp.Request()
		writer := rp.Writer()

		// Log the metrics
		slog.Debug("HTTP request metrics",
			"method", req.Method,
			"path", req.URL.Path,
			"status", writer.Status(),
			"duration_ms", duration.Milliseconds(),
		)
	}
}
