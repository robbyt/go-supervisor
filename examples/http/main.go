package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/logger"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/metrics"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/recovery"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/wildcard"
	"github.com/robbyt/go-supervisor/supervisor"
)

const (
	// Port the http server binds to
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

	wildcardHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "You requested: %s\n", r.URL.Path)
	}

	// Create middleware for the routes using the new middleware system
	loggingMw := logger.New(logHandler.WithGroup("http"))
	recoveryMw := recovery.New(logHandler.WithGroup("recovery"))
	metricsMw := metrics.New()

	// Common middleware stack for all routes (using new HandlerFunc pattern)
	commonMw := []httpserver.HandlerFunc{
		recoveryMw,
		loggingMw,
		metricsMw,
	}

	// Create routes with common middleware attached to each
	indexRoute, err := httpserver.NewRouteFromHandlerFunc(
		"index",
		"/",
		indexHandler,
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create index route: %w", err)
	}

	// Status route to provide a health check
	statusRoute, err := httpserver.NewRouteFromHandlerFunc(
		"status",
		"/status",
		statusHandler,
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create status route: %w", err)
	}

	// API wildcard route using the new middleware system
	apiRoute, err := httpserver.NewRouteFromHandlerFunc(
		"api",
		"/api/*",
		wildcardHandler,
		wildcard.New("/api/"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create wildcard route: %w", err)
	}

	return httpserver.Routes{*indexRoute, *statusRoute, *apiRoute}, nil
}

// RunServer initializes and runs the HTTP server with supervisor
func RunServer(
	ctx context.Context,
	logHandler slog.Handler,
	routes []httpserver.Route,
) (*supervisor.PIDZero, error) {
	// Create a config callback function that will be used by the runner
	configCallback := func() (*httpserver.Config, error) {
		return httpserver.NewConfig(ListenOn, routes, httpserver.WithDrainTimeout(DrainTimeout))
	}

	// Create the HTTP server runner
	runner, err := httpserver.NewRunner(
		httpserver.WithConfigCallback(configCallback),
		httpserver.WithLogHandler(logHandler))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP server runner: %w", err)
	}

	// Create a PIDZero supervisor and add the runner
	sv, err := supervisor.New(
		supervisor.WithContext(ctx),
		supervisor.WithLogHandler(logHandler),
		supervisor.WithRunnables(runner))
	if err != nil {
		return nil, fmt.Errorf("failed to create supervisor: %w", err)
	}

	return sv, nil
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

	sv, err := RunServer(ctx, handler, routes)
	if err != nil {
		slog.Error("Failed to setup server", "error", err)
		os.Exit(1)
	}

	// Start the supervisor - this will block until shutdown
	slog.Info("Starting supervisor with HTTP server on " + ListenOn)
	if err := sv.Run(); err != nil {
		slog.Error("Supervisor failed", "error", err)
		os.Exit(1)
	}
}
