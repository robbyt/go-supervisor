// Package main demonstrates a JSON API server using go-supervisor with proper
// middleware layering and separation of concerns.
//
// # Middleware Architecture
//
// This example shows how to properly layer middleware with clean separation of concerns:
//
// 1. JSON Enforcer Middleware (examples/jsonapi/middleware):
//   - Only transforms response body content to JSON format
//   - Does NOT set any headers
//   - Wraps non-JSON responses in {"response": "content"}
//   - Preserves valid JSON responses as-is
//
// 2. Headers Middleware (runnables/httpserver/middleware/headers):
//   - Exclusively handles all HTTP header management
//   - Sets Content-Type, CORS, security headers, etc.
//   - Can be overridden by handlers if needed
//
// 3. Middleware Execution Order:
//   - Request flow: recovery -> security -> logging -> metrics -> headers -> handler
//   - Response flow: handler -> headers -> metrics -> logging -> security -> recovery
//   - Order is critical: recovery must be outermost, headers should be innermost
//
// This separation ensures each middleware has a single responsibility and they
// compose cleanly without conflicts.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/robbyt/go-supervisor/examples/custom_middleware/example"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	headersMw "github.com/robbyt/go-supervisor/runnables/httpserver/middleware/headers"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/logger"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/metrics"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/recovery"
	"github.com/robbyt/go-supervisor/supervisor"
)

const (
	// Port the JSON API server binds to
	ListenOn = ":8081"

	// How long the supervisor waits for the HTTP server to drain before forcefully shutting down
	DrainTimeout = 5 * time.Second
)

// buildRoutes sets up various HTTP routes for this JSON API example server
func buildRoutes(logHandler slog.Handler) ([]httpserver.Route, error) {
	// Create middleware stack
	l := slog.New(logHandler)
	recoveryMw := recovery.New(l.WithGroup("recovery"))
	securityMw := headersMw.Security()
	loggingMw := logger.New(l.WithGroup("example"))
	metricsMw := metrics.New()

	// Sets JSON response headers
	jsonHeadersMw := headersMw.JSON()

	// Common middleware stack for all routes
	// Order matters! middleware executes in order on request, reverse order on response
	commonMw := []httpserver.HandlerFunc{
		recoveryMw,    // 1. Handle panics first - MUST be outermost to catch all failures
		securityMw,    // 2. Add security headers early - ensures they're always set
		loggingMw,     // 3. Log requests - captures what's actually being processed
		metricsMw,     // 4. Collect metrics - measures logged requests
		jsonHeadersMw, // 5. Set JSON headers last - prevents handler override
	}

	// CORS middleware for API endpoints
	corsMw := headersMw.CORS("*", "GET,POST,PUT,DELETE,OPTIONS", "Content-Type,Authorization")

	// API-specific middleware (adds CORS after security headers)
	apiMw := []httpserver.HandlerFunc{
		commonMw[0], // recoveryMw
		commonMw[1], // securityMw
		corsMw,      // different from commonMw - CORS for API endpoints
		commonMw[2], // loggingMw
		commonMw[3], // metricsMw
		commonMw[4], // jsonHeadersMw
	}

	// Index handler and route creation - plain text will be converted to JSON
	indexHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome to the JSON API example server!")
	}
	indexRoute, err := httpserver.NewRouteFromHandlerFunc(
		"index",
		"/",
		example.WrapHandlerForJSON(indexHandler),
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create index route: %w", err)
	}

	// JSON handler and route creation - will be preserved as-is
	jsonHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(
			w,
			`{"message": "This is already JSON", "status": "success", "timestamp": "%s"}`,
			time.Now().Format(time.RFC3339),
		)
	}
	jsonRoute, err := httpserver.NewRouteFromHandlerFunc(
		"json-data",
		"/api/data",
		example.WrapHandlerForJSON(jsonHandler),
		apiMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create JSON route: %w", err)
	}

	// HTML handler and route creation - will be converted to JSON
	htmlHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(
			w,
			`<html><body><h1>Hello from HTML</h1><p>This will be converted to JSON!</p></body></html>`,
		)
	}
	htmlRoute, err := httpserver.NewRouteFromHandlerFunc(
		"html-demo",
		"/html",
		example.WrapHandlerForJSON(htmlHandler),
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTML route: %w", err)
	}

	// Error handler and route creation - demonstrates a 404 error response in JSON format
	errorHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "The requested resource was not found")
	}
	errorRoute, err := httpserver.NewRouteFromHandlerFunc(
		"error-demo",
		"/error",
		example.WrapHandlerForJSON(errorHandler),
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create error route: %w", err)
	}

	// Panic handler and route creation - demonstrates panic recovery
	panicHandler := func(w http.ResponseWriter, r *http.Request) {
		panic("Whoops! This is a simulated panic")
	}
	panicRoute, err := httpserver.NewRouteFromHandlerFunc(
		"panic-demo",
		"/panic",
		example.WrapHandlerForJSON(panicHandler),
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create panic route: %w", err)
	}

	return httpserver.Routes{*indexRoute, *jsonRoute, *htmlRoute, *errorRoute, *panicRoute}, nil
}

func createHTTPServer(
	routes []httpserver.Route,
	logHandler slog.Handler,
) (*httpserver.Runner, error) {
	// Create the HTTP server config
	config, err := httpserver.NewConfig(ListenOn, routes, httpserver.WithDrainTimeout(DrainTimeout))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP server config: %w", err)
	}

	// Create the HTTP server runner
	return httpserver.NewRunner(
		httpserver.WithConfig(config),
		httpserver.WithLogHandler(logHandler),
	)
}

// createSupervisor initializes go-supervisor with the provided runnable
func createSupervisor(
	ctx context.Context,
	logHandler slog.Handler,
	runnable supervisor.Runnable,
) (*supervisor.PIDZero, error) {
	// Create a PIDZero supervisor and add the runner
	sv, err := supervisor.New(
		supervisor.WithContext(ctx),
		supervisor.WithLogHandler(logHandler),
		supervisor.WithRunnables(runnable))
	if err != nil {
		return nil, fmt.Errorf("failed to create supervisor: %w", err)
	}

	return sv, nil
}

func main() {
	// Configure the logger
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	// Create base context
	ctx := context.Background()

	// Build routes
	routes, err := buildRoutes(handler)
	if err != nil {
		slog.Error("Failed to build routes", "error", err)
		os.Exit(1)
	}

	// Create the HTTP server with the defined routes
	httpServer, err := createHTTPServer(routes, handler)
	if err != nil {
		slog.Error("Failed to create HTTP server", "error", err)
		os.Exit(1)
	}

	// Create the supervisor with the HTTP server and routes
	sv, err := createSupervisor(ctx, handler, httpServer)
	if err != nil {
		slog.Error("Failed to setup server", "error", err)
		os.Exit(1)
	}

	// Start the supervisor - this will block until shutdown
	slog.Info("Starting JSON API example server", "listen", ListenOn)
	slog.Info("Available endpoints:")
	slog.Info("  GET / - Plain text response (responses will be converted to JSON)")
	slog.Info("  GET /api/data - JSON response (existing format is preserved)")
	slog.Info("  GET /html - HTML response (HTML blob embedded in JSON)")
	slog.Info("  GET /error - Error response (error messages always converted to JSON)")
	slog.Info("  GET /panic - Triggers a panic to demonstrate recovery middleware")

	if err := sv.Run(); err != nil {
		slog.Error("Supervisor failed", "error", err)
		os.Exit(1)
	}
}
