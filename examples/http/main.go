// Package main demonstrates a basic HTTP server using go-supervisor.
// This example shows the core functionality of the httpserver runnable:
//
//   - Creating simple HTTP routes with handlers
//   - Setting up the HTTP server with go-supervisor
//   - Graceful shutdown and configuration reloading
//   - Clean separation between route definition and server management
//
// For examples with middleware chains, custom error handling, and
// advanced features, see the custom_middleware example.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/supervisor"
)

const (
	// Port the http server binds to
	ListenOn = ":8080"

	// How long the supervisor waits for the HTTP server to drain before forcefully shutting down
	DrainTimeout = 5 * time.Second
)

// buildRoutes sets up basic HTTP routes for this example server
func buildRoutes() ([]httpserver.Route, error) {
	// Index handler and route creation
	indexHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Welcome to the go-supervisor example HTTP server!\n")
	}
	indexRoute, err := httpserver.NewRouteFromHandlerFunc("index", "/", indexHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create index route: %w", err)
	}

	// Status handler and route creation - simple health check
	statusHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Status: OK\n")
	}
	statusRoute, err := httpserver.NewRouteFromHandlerFunc("status", "/status", statusHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create status route: %w", err)
	}

	// About handler and route creation
	aboutHandler := func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "This is a basic HTTP server example using go-supervisor.\n")
		fmt.Fprintf(w, "It demonstrates:\n")
		fmt.Fprintf(w, "- Simple route handling\n")
		fmt.Fprintf(w, "- Graceful shutdown\n")
		fmt.Fprintf(w, "- Configuration reloading\n")
	}
	aboutRoute, err := httpserver.NewRouteFromHandlerFunc("about", "/about", aboutHandler)
	if err != nil {
		return nil, fmt.Errorf("failed to create about route: %w", err)
	}

	// Return the slice of routes
	return httpserver.Routes{*indexRoute, *statusRoute, *aboutRoute}, nil
}

func createHTTPServer(
	routes []httpserver.Route,
	logHandler slog.Handler,
) (*httpserver.Runner, error) {
	// Create a config callback function that will be used by the runner
	configCallback := func() (*httpserver.Config, error) {
		return httpserver.NewConfig(ListenOn, routes, httpserver.WithDrainTimeout(DrainTimeout))
	}

	// Create the HTTP server runner
	return httpserver.NewRunner(
		httpserver.WithConfigCallback(configCallback),
		httpserver.WithLogHandler(logHandler))
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
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(handler))

	// Create base context
	ctx := context.Background()

	// Build routes
	routes, err := buildRoutes()
	if err != nil {
		slog.Error("Failed to build routes", "error", err)
		os.Exit(1)
	}

	// Create the HTTP server
	httpServer, err := createHTTPServer(routes, handler)
	if err != nil {
		slog.Error("Failed to create HTTP server", "error", err)
		os.Exit(1)
	}

	// Create the supervisor
	sv, err := createSupervisor(ctx, handler, httpServer)
	if err != nil {
		slog.Error("Failed to setup server", "error", err)
		os.Exit(1)
	}

	// Start the supervisor - this will block until shutdown
	slog.Info("Starting HTTP server", "address", ListenOn)
	slog.Info("Try these endpoints:",
		"index", "http://localhost:8080/",
		"status", "http://localhost:8080/status",
		"about", "http://localhost:8080/about")

	if err := sv.Run(); err != nil {
		slog.Error("Server stopped", "error", err)
		os.Exit(1)
	}
}
