package main

import (
	"context"
	"fmt"
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

	// Create a modified version of RunServer with a custom listen address
	runServerWithCustomPort := func(ctx context.Context, logHandler slog.Handler, listenAddr string) (*supervisor.PIDZero, func(), error) {
		rootLogger := slog.New(logHandler)

		// Create HTTP handlers functions (same as RunServer)
		handlerLogger := rootLogger.WithGroup("httpserver")
		indexHandler := func(w http.ResponseWriter, r *http.Request) {
			handlerLogger.Info("Handling index request", "path", r.URL.Path)
			_, err := io.WriteString(w, "Welcome to the go-supervisor example HTTP server!\n")
			if err != nil {
				handlerLogger.Error("Failed to write response", "error", err)
			}
		}

		statusHandler := func(w http.ResponseWriter, r *http.Request) {
			handlerLogger.Info("Handling status request", "path", r.URL.Path)
			_, err := io.WriteString(w, "Status: OK\n")
			if err != nil {
				handlerLogger.Error("Failed to write response", "error", err)
			}
		}

		wildcardHandler := func(w http.ResponseWriter, r *http.Request) {
			handlerLogger.Info("Handling wildcard request", "path", r.URL.Path)
			_, err := io.WriteString(w, "You requested: "+r.URL.Path+"\n")
			if err != nil {
				handlerLogger.Error("Failed to write response", "error", err)
			}
		}

		// Create routes (same as RunServer)
		indexRoute, err := httpserver.NewRoute("index", "/", indexHandler)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create index route: %w", err)
		}

		statusRoute, err := httpserver.NewRoute("status", "/status", statusHandler)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create status route: %w", err)
		}

		apiRoute, err := httpserver.NewWildcardRoute("/api", wildcardHandler)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create wildcard route: %w", err)
		}

		routes := httpserver.Routes{*indexRoute, *statusRoute, *apiRoute}

		// Create a config callback function with CUSTOM listen address
		configCallback := func() (*httpserver.Config, error) {
			return httpserver.NewConfig(listenAddr, DrainTimeout, routes)
		}

		// Create the HTTP server runner
		customCtx, customCancel := context.WithCancel(ctx)

		runner, err := httpserver.NewRunner(
			httpserver.WithContext(customCtx),
			httpserver.WithConfigCallback(configCallback),
			httpserver.WithLogHandler(logHandler.WithGroup("httpserver")))
		if err != nil {
			customCancel()
			return nil, nil, fmt.Errorf("failed to create HTTP server runner: %w", err)
		}

		// Create supervisor
		sv, err := supervisor.New(
			supervisor.WithContext(ctx),
			supervisor.WithRunnables(runner),
			supervisor.WithLogHandler(logHandler))
		if err != nil {
			customCancel()
			return nil, nil, fmt.Errorf("failed to create supervisor: %w", err)
		}

		// Create a cleanup function
		cleanup := func() {
			rootLogger.Info("Cleaning up resources")
			customCancel()
		}

		return sv, cleanup, nil
	}

	// Create a test logger that discards output
	testHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})

	// Create a context
	ctx := context.Background()

	// Run with invalid port - should fail
	sv, cleanup, err := runServerWithCustomPort(ctx, testHandler, ":-1")
	if err == nil {
		// If it somehow didn't fail at creation, it will fail when we run it
		if cleanup != nil {
			defer cleanup()
		}
		if sv != nil {
			err = sv.Run()
			assert.Error(t, err, "Run should fail with invalid port")
		}
	} else {
		// Expected case - creation should fail with invalid port
		assert.Contains(t, err.Error(), "invalid")
	}
}
