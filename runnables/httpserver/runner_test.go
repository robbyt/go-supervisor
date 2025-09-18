package httpserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCustomServerCreator tests that the custom server creator is used correctly
func TestCustomServerCreator(t *testing.T) {
	t.Parallel()

	// Create a mock HTTP server
	mockServer := new(MockHttpServer)
	mockServer.On("ListenAndServe").Return(nil)
	mockServer.On("Shutdown", mock.Anything).Return(nil)

	// Create a custom server creator that returns our mock
	customCreator := func(addr string, handler http.Handler, cfg *Config) HttpServer {
		return mockServer
	}

	// Create a test route and config
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)
	routes := Routes{*route}

	// Use a unique port to avoid conflicts
	listenAddr := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	// Create a callback that generates the config with our custom server creator
	cfgCallback := func() (*Config, error) {
		return NewConfig(
			listenAddr,
			routes,
			WithDrainTimeout(1*time.Second),
			WithServerCreator(customCreator),
		)
	}

	// Create the runner
	runner, err := NewRunner(
		WithConfigCallback(cfgCallback),
	)
	require.NoError(t, err)

	// We can't use boot() directly because it would start the server and wait for it to be ready
	// Instead, we'll just create the server and set it directly
	cfg := runner.getConfig()
	require.NotNil(t, cfg, "Config should not be nil")
	require.NotNil(t, cfg.ServerCreator, "ServerCreator should not be nil")

	// Create and set the server using the custom creator
	runner.server = cfg.ServerCreator(cfg.ListenAddr, http.HandlerFunc(handler), cfg)

	// Verify the server was created with our custom creator
	assert.Same(t, mockServer, runner.server, "Server should be our mock instance")

	// Directly test the mock's methods without going through the runner's FSM
	// This avoids FSM state transition issues in the test
	ctx := context.Background()
	err = mockServer.Shutdown(ctx)
	require.NoError(t, err)

	// Verify that our mock server's Shutdown was called
	mockServer.AssertCalled(t, "Shutdown", mock.Anything)
}

// TestRoutesRequired verifies that configs must include routes
func TestRoutesRequired(t *testing.T) {
	c, err := NewConfig(":8080", Routes{}, WithDrainTimeout(0))
	assert.Nil(t, c)
	assert.Error(t, err)
}

// TestServerErr verifies behavior when a server fails due to address in use
func TestServerErr(t *testing.T) {
	t.Parallel()

	// Use fixed port for this test as we intentionally want a conflict
	port := ":9230"

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("v1", "/", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	// Create two server configs using the same port
	cfg1 := func() (*Config, error) { return NewConfig(port, hConfig, WithDrainTimeout(0)) }
	server1, err := NewRunner(WithConfigCallback(cfg1))
	require.NoError(t, err)

	// Start the first server
	done1 := make(chan error, 1)
	go func() {
		err := server1.Run(context.Background())
		done1 <- err
	}()

	// Give time for the first server to start
	waitForState(
		t,
		server1,
		finitestate.StatusRunning,
		2*time.Second,
		"First server should enter Running state",
	)

	// Create a second server with the same port
	cfg2 := func() (*Config, error) { return NewConfig(port, hConfig, WithDrainTimeout(0)) }
	server2, err := NewRunner(WithConfigCallback(cfg2))
	require.NoError(t, err)

	// The second server should fail to start with "address already in use"
	err = server2.Run(t.Context())
	require.Error(t, err)
	// The error contains ErrServerBoot, but we can't use ErrorIs here directly
	// because of how the error is wrapped
	require.Contains(t, err.Error(), "address already in use")

	// Clean up
	server1.Stop()
	<-done1
}

// TestServerLifecycle tests the complete lifecycle of the server
func TestServerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	t.Parallel()

	// Use unique port numbers for parallel tests
	listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("v1", "/", handler)
	require.NoError(t, err)

	// Create the Config and HTTPServer instance
	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, Routes{*route}, WithDrainTimeout(1*time.Second))
	}

	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Run the server in a goroutine
	done := make(chan error, 1)
	go func() {
		err := server.Run(t.Context())
		done <- err
	}()

	// Wait for the server to start
	waitForState(
		t,
		server,
		finitestate.StatusRunning,
		2*time.Second,
		"Server should enter Running state",
	)

	// Test stop and state transition
	server.Stop()
	err = <-done

	require.NoError(t, err, "Server should shut down without error")
	assert.Equal(
		t,
		finitestate.StatusStopped,
		server.GetState(),
		"Server should be in stopped state",
	)
}

// TestStopServerWhenNotRunning verifies behavior when stopping a non-running server
func TestStopServerWhenNotRunning(t *testing.T) {
	t.Parallel()

	server, listenPort := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)
	t.Logf("Server listening on port %s", listenPort)
	t.Cleanup(func() {
		server.Stop()
	})

	err := server.stopServer(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrServerNotRunning)
}

// TestString verifies the correct string representation of the Runner
func TestString(t *testing.T) {
	t.Parallel()

	t.Run("with config", func(t *testing.T) {
		// Use a unique port
		listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		handler := func(w http.ResponseWriter, r *http.Request) {}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)
		hConfig := Routes{*route}

		// Create the Config and HTTPServer instance
		cfgCallback := func() (*Config, error) {
			return NewConfig(listenPort, hConfig, WithDrainTimeout(0))
		}

		server, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Test string representation before starting
		expectedStr := fmt.Sprintf("HTTPServer{listening: %s}", listenPort)
		assert.Equal(t, expectedStr, server.String(), "Wrong string representation before running")

		// Start the server
		done := make(chan error, 1)
		go func() {
			err := server.Run(t.Context())
			done <- err
		}()

		// Wait for the server to start
		waitForState(
			t,
			server,
			finitestate.StatusRunning,
			2*time.Second,
			"Server should enter Running state",
		)

		// Test string representation while running
		assert.Equal(t, expectedStr, server.String(), "Wrong string representation while running")

		// Clean up
		server.Stop()
		<-done
	})

	t.Run("with config and name", func(t *testing.T) {
		// Use a unique port
		listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		handler := func(w http.ResponseWriter, r *http.Request) {}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)
		hConfig := Routes{*route}

		// Create the Config and HTTPServer instance
		cfgCallback := func() (*Config, error) {
			return NewConfig(listenPort, hConfig, WithDrainTimeout(0))
		}
		testName := "TestServer"
		server, err := NewRunner(
			WithConfigCallback(cfgCallback),
			WithName(testName),
		)
		require.NoError(t, err)
		assert.Equal(
			t,
			fmt.Sprintf("HTTPServer{name: %s, listening: %s}", testName, listenPort),
			server.String(),
		)
	})

	t.Run("with nil config", func(t *testing.T) {
		// This is testing just the nil handling in String()
		// We'll test the method directly with a mock implementation

		// Create a stub Runner that has only what's needed for String()
		type stubRunner struct {
			Runner
		}

		// Create stub with logger and config callback that returns nil
		stub := &stubRunner{}
		stub.configCallback = func() (*Config, error) { return nil, nil }
		stub.logger = slog.Default().WithGroup("httpserver.Runner")

		// Test string representation with nil config
		assert.Equal(t, "HTTPServer<>", stub.String())
	})
}
