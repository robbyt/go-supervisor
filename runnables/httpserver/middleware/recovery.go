package middleware

import (
	"log/slog"
	"net/http"
)

// PanicRecovery creates a middleware that recovers from panics in HTTP handlers.
// It logs the error and returns a 500 Internal Server Error response.
func PanicRecovery(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default().WithGroup("httpserver")
	}

	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("HTTP handler panic recovered",
						"error", err,
						"path", r.URL.Path,
						"method", r.Method,
					)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()

			next(w, r)
		}
	}
}
