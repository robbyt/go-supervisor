// Package httpserver_test contains compile-checked mirrors of the code
// snippets in README.md. If you edit a snippet under "Creating Custom
// Middleware" (or add a new one), update or add the matching Example
// function below so `go test` will catch typos and signature drift.
//
// Each Example function is named after the README subsection it mirrors;
// no Output comment is needed because we only care that the snippets
// compile against the current httpserver API.
package httpserver_test

import (
	"net/http"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// Mirrors the "Two-Phase Execution" snippet that sets X-Process-Time.
func newTimingMiddleware() httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		start := time.Now()

		rp.Next()

		duration := time.Since(start)
		rp.Writer().Header().Set("X-Process-Time", duration.String())
	}
}

func ExampleHandlerFunc_timing() {
	_ = newTimingMiddleware()
}

// Stub matching the README's reference to an authorization helper.
func readmeIsAuthorized(_ *http.Request, _ string) bool { return true }

// Mirrors the "When to Abort" AuthMiddleware snippet.
func newAuthMiddleware(requiredRole string) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		if !readmeIsAuthorized(rp.Request(), requiredRole) {
			http.Error(rp.Writer(), "Forbidden", http.StatusForbidden)
			rp.Abort()
			return
		}
		rp.Next()
	}
}

func ExampleHandlerFunc_auth() {
	_ = newAuthMiddleware("admin")
}

// readmeRecorder mirrors the Recorder interface in the README's
// "Example: Metrics Middleware" subsection.
type readmeRecorder interface {
	Observe(method, path string, status int, duration time.Duration)
}

func newMetricsMiddleware(rec readmeRecorder) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		start := time.Now()

		rp.Next()

		rec.Observe(
			rp.Request().Method,
			rp.Request().URL.Path,
			rp.Writer().Status(),
			time.Since(start),
		)
	}
}

func ExampleHandlerFunc_metrics() {
	var rec readmeRecorder
	_ = newMetricsMiddleware(rec)
}
