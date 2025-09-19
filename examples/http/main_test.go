package main

import (
	"context"
	"errors"
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

	sv, err := RunServer(ctx, logHandler, routes)
	require.NoError(t, err, "RunServer should not return an error")
	require.NotNil(t, sv, "Supervisor should not be nil")

	// Start the server in a goroutine to avoid blocking the test
	errCh := make(chan error, 1)
	go func() {
		errCh <- sv.Run()
	}()

	// Wait for the server to be ready by checking if it responds to requests
	assert.Eventually(t, func() bool {
		resp, err := http.Get("http://localhost:8080/status")
		if err != nil {
			return false
		}
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 50*time.Millisecond, "Server should become ready")

	// Make a request to the server
	resp, err := http.Get("http://localhost:8080/status")
	require.NoError(t, err, "Failed to make GET request")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Failed to read response body")
	assert.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Status: OK\n", string(body))

	// Stop the supervisor
	sv.Shutdown()

	// Wait for Run() to complete and check the result
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			require.NoError(t, err, "Run() should not return an error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() should have completed within timeout")
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

	err = sv.Run()
	require.Error(t, err, "Run() should fail with invalid port")
	assert.ErrorIs(t,
		err, httpserver.ErrServerBoot,
		"httpserver.Runner.Run() should return ErrServerBoot",
	)
}
