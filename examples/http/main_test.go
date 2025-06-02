package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildRoutes tests that routes are created correctly
func TestBuildRoutes(t *testing.T) {
	t.Parallel()

	routes, err := buildRoutes()
	require.NoError(t, err, "buildRoutes should not fail")
	require.Len(t, routes, 3, "should have 3 routes")

	// Test each route
	for _, route := range routes {
		// Create a test server for the route
		req := httptest.NewRequest("GET", route.Path, nil)
		rec := httptest.NewRecorder()

		route.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code, "route %s should return 200", route.Path)
		assert.NotEmpty(t, rec.Body.String(), "route %s should return content", route.Path)
	}
}

// TestCreateHTTPServer tests that the HTTP server can be created
func TestCreateHTTPServer(t *testing.T) {
	t.Parallel()

	// Create a test logger that discards output
	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})

	// Build routes
	routes, err := buildRoutes()
	require.NoError(t, err, "Failed to build routes")

	// Create HTTP server
	httpServer, err := createHTTPServer(routes, logHandler)
	require.NoError(t, err, "Failed to create HTTP server")
	require.NotNil(t, httpServer, "HTTP server should not be nil")
}

// TestRunServerInvalidPort tests error handling when an invalid port is specified
func TestRunServerInvalidPort(t *testing.T) {
	t.Parallel()

	// Create a test logger that discards output
	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})

	// Create a context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build routes - reuse the same routes from the main app
	routes, err := buildRoutes()
	require.NoError(t, err, "Failed to build routes")

	// Create a config callback that uses an invalid port
	configCallback := func() (*httpserver.Config, error) {
		return httpserver.NewConfig(":-1", routes, httpserver.WithDrainTimeout(DrainTimeout))
	}

	// Create HTTP server runner with invalid port
	runner, err := httpserver.NewRunner(
		httpserver.WithConfigCallback(configCallback),
		httpserver.WithLogHandler(logHandler.WithGroup("httpserver")),
	)
	require.NoError(t, err, "Should be able to create runner even with invalid config")

	// Create supervisor with a reasonable timeout
	sv, err := supervisor.New(
		supervisor.WithContext(ctx),
		supervisor.WithRunnables(runner),
		supervisor.WithLogHandler(logHandler),
		supervisor.WithStartupTimeout(100*time.Millisecond), // Short timeout for tests
	)
	require.NoError(t, err, "Failed to create supervisor")

	// Run the supervisor - should fail because of invalid port
	err = sv.Run()
	assert.Error(t, err, "Run should fail with invalid port")
	assert.Contains(t, err.Error(), "timeout waiting for runnable to start")
}
