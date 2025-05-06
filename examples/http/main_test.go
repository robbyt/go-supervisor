package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunServer tests that the HTTP server starts successfully
func TestRunServer(t *testing.T) {
	t.Parallel()

	// Create a test logger that discards output
	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})

	// Create a context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run the server
	routes, err := buildRoutes(logHandler)
	require.NoError(t, err, "Failed to build routes")
	require.NotEmpty(t, routes, "Routes should not be empty")

	sv, cleanup, err := RunServer(ctx, logHandler, routes)
	require.NoError(t, err, "RunServer should not return an error")
	require.NotNil(t, sv, "Supervisor should not be nil")
	require.NotNil(t, cleanup, "Cleanup function should not be nil")

	// Start the server in a goroutine to avoid blocking the test
	errCh := make(chan error, 1)
	go func() {
		errCh <- sv.Run()
	}()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	// Make a request to the server
	resp, err := http.Get("http://localhost:8080/status")
	require.NoError(t, err, "Failed to make GET request")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Failed to read response body")
	assert.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Status: OK\n", string(body))

	// Clean up
	cleanup()

	// Check that Run() didn't return an error
	select {
	case err := <-errCh:
		assert.NoError(t, err, "Run() should not return an error")
	case <-time.After(100 * time.Millisecond):
		// This is expected - the server is still running
	}
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
	routes, err := buildRoutes(logHandler)
	require.NoError(t, err, "Failed to build routes")

	// Create a config callback that uses an invalid port
	configCallback := func() (*httpserver.Config, error) {
		return httpserver.NewConfig(":-1", routes, httpserver.WithDrainTimeout(DrainTimeout))
	}

	// Create HTTP server runner with invalid port
	runner, err := httpserver.NewRunner(
		httpserver.WithContext(ctx),
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
