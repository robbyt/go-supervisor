package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// MetricCollector creates a middleware that collects metrics about HTTP requests.
// This is a placeholder implementation that can be extended to integrate with
// your metrics collection system.
func MetricCollector() Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a response writer wrapper to capture status code
			rw := &ResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK, // Default status code
			}

			// Call the next handler in the chain
			next(rw, r)

			// Record metrics
			duration := time.Since(start)

			// TODO: Actually record metrics using a metrics library like prometheus
			// Example:
			// httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(rw.statusCode)).Inc()
			// httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration.Seconds())

			// Log the metrics
			slog.Debug("HTTP request metrics",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.statusCode,
				"duration_ms", duration.Milliseconds(),
			)
		}
	}
}
