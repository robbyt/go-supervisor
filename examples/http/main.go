package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware"
	"github.com/robbyt/go-supervisor/supervisor"
)

const (
	// Port the http server should bind to
	ListenOn = ":8080"

	// How long the supervisor waits for the HTTP server to drain before forcefully shutting down
	DrainTimeout = 5 * time.Second
)

// buildRoutes will setup various HTTP routes for this example server
func buildRoutes(logHandler slog.Handler) ([]httpserver.Route, error) {
	// Create HTTP handlers functions
	indexHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome to the go-supervisor example HTTP server!\n")
	}

	statusHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Status: OK\n")
	}

	// Add a metrics handler that shows server state
	metricsHandler := func(w http.ResponseWriter, r *http.Request) {
		// In a real implementation, this would include actual metrics
		// like request count, response times, etc.
		fmt.Fprintf(w, "# HELP server_state Current state of the HTTP server\n")
		fmt.Fprintf(w, "# TYPE server_state gauge\n")
		fmt.Fprintf(w, "server_state{name=\"http_server\"} 1 # running\n")
	}

	wildcardHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "You requested: %s\n", r.URL.Path)
	}

	// Create middleware for the routes
	logger := slog.New(logHandler)
	loggingMw := middleware.Logger(logger.WithGroup("http"))
	recoveryMw := middleware.PanicRecovery(logger.WithGroup("recovery"))
	metricsMw := middleware.MetricCollector()

	// Common middleware stack for all routes
	commonMw := []middleware.Middleware{
		recoveryMw,
		loggingMw,
		metricsMw,
	}

	// Create routes with common middleware attached to each
	indexRoute, err := httpserver.NewRouteWithMiddleware(
		"index",
		"/",
		indexHandler,
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create index route: %w", err)
	}

	// Status route to provide a health check
	statusRoute, err := httpserver.NewRouteWithMiddleware(
		"status",
		"/status",
		statusHandler,
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create status route: %w", err)
	}

	// Add metrics endpoint with middleware
	metricsRoute, err := httpserver.NewRouteWithMiddleware(
		"metrics",
		"/metrics",
		metricsHandler,
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics route: %w", err)
	}

	// API routes need their middlewares passed explicitly with the updated function
	apiRoute, err := httpserver.NewWildcardRoute("/api", wildcardHandler, commonMw...)
	if err != nil {
		return nil, fmt.Errorf("failed to create wildcard route: %w", err)
	}

	return httpserver.Routes{*indexRoute, *statusRoute, *metricsRoute, *apiRoute}, nil
}

// RunServer initializes and runs the HTTP server with supervisor
// Returns the supervisor and a cleanup function
func RunServer(ctx context.Context, logHandler slog.Handler, routes []httpserver.Route) (*supervisor.PIDZero, func(), error) {
	// Create a config callback function that will be used by the runner
	configCallback := func() (*httpserver.Config, error) {
		return httpserver.NewConfig(ListenOn, DrainTimeout, routes)
	}

	// Create the HTTP server runner with a custom context
	customCtx, customCancel := context.WithCancel(ctx)

	runner, err := httpserver.NewRunner(
		httpserver.WithContext(customCtx),
		httpserver.WithConfigCallback(configCallback),
		httpserver.WithLogHandler(logHandler))
	if err != nil {
		customCancel()
		return nil, nil, fmt.Errorf("failed to create HTTP server runner: %w", err)
	}

	// Create a PIDZero supervisor and add the runner
	sv, err := supervisor.New(
		supervisor.WithContext(ctx),
		supervisor.WithRunnables(runner),
		supervisor.WithLogHandler(logHandler))
	if err != nil {
		customCancel()
		return nil, nil, fmt.Errorf("failed to create supervisor: %w", err)
	}

	// Create a cleanup function for proper teardown
	cleanup := func() {
		customCancel()
	}

	return sv, cleanup, nil
}

func main() {
	// Configure the custom logger
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		// AddSource: true,
	})
	slog.SetDefault(slog.New(handler))

	// Create base context
	ctx := context.Background()

	// Run the server
	routes, err := buildRoutes(handler)
	if err != nil {
		slog.Error("Failed to build routes", "error", err)
		os.Exit(1)
	}

	sv, cleanup, err := RunServer(ctx, handler, routes)
	if err != nil {
		slog.Error("Failed to setup server", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	// Start the supervisor - this will block until shutdown
	slog.Info("Starting supervisor with HTTP server on " + ListenOn)
	if err := sv.Run(); err != nil {
		slog.Error("Supervisor failed", "error", err)
		os.Exit(1)
	}
}
