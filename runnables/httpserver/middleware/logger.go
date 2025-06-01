package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// Logger creates a middleware that logs information about HTTP requests.
// It logs the request method, path, status code, and response time.
func Logger(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default().WithGroup("httpserver")
	}

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

			// Log request information
			duration := time.Since(start)
			logger.Info("HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rw.statusCode,
				"duration", duration,
				"user_agent", r.UserAgent(),
				"remote_addr", r.RemoteAddr,
			)
		}
	}
}
