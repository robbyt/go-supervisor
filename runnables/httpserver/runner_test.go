package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitForRunningState waits for the server to enter the Running state
// or fails the test if the server doesn't enter the Running state within the timeout
func waitForRunningState(t *testing.T, server *Runner, timeout time.Duration) {
	t.Helper()
	startTime := time.Now()

	for {
		if time.Since(startTime) > timeout {
			t.Fatalf(
				"Server did not reach Running state in time. Current state: %s",
				server.GetState(),
			)
		}

		if server.GetState() == finitestate.StatusRunning {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}
}

// TestRun_ShutdownWithDrainTimeout tests that the server waits for handlers to complete
// within the drain timeout period
func TestRun_ShutdownWithDrainTimeout(t *testing.T) {
	t.Parallel()

	// Create test server with a handler that sleeps
	const sleepDuration = 1 * time.Second
	const drainTimeout = 3 * time.Second

	// Use unique port to avoid conflicts
	listenPort := getAvailablePort(t, 9000)
	started := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(sleepDuration)
		w.WriteHeader(http.StatusOK)
	}

	// Create the server manually
	route, err := NewRoute("v1", "/sleep", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, drainTimeout, hConfig)
	}

	server, err := NewRunner(WithContext(context.Background()), WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Channel to capture Run's completion
	done := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		err := server.Run(context.Background())
		done <- err
	}()

	// Wait for the server to enter the Running state
	waitForRunningState(t, server, 2*time.Second)

	// Make a request to trigger the handler
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://localhost%s/sleep", listenPort))
		if err == nil {
			assert.NoError(t, resp.Body.Close())
		}
	}()

	// Wait for handler to start, then initiate shutdown
	select {
	case <-started:
		server.Stop()
	case <-time.After(1 * time.Second):
		t.Fatal("Handler did not start in time")
	}

	// Measure shutdown time
	start := time.Now()
	err = <-done
	elapsed := time.Since(start)

	// Verify shutdown behavior
	require.NoError(t, err)
	require.GreaterOrEqual(t, elapsed.Seconds(), sleepDuration.Seconds(),
		"Shutdown did not wait for the handler")
	require.LessOrEqual(t, elapsed.Seconds(), drainTimeout.Seconds()+0.5,
		"Shutdown exceeded expected time")
}

// TestRun_ShutdownDeadlineExceeded tests shutdown behavior when a handler exceeds the drain timeout
func TestRun_ShutdownDeadlineExceeded(t *testing.T) {
	t.Parallel()

	// Create test server with a handler that exceeds the drain timeout
	const sleepDuration = 5 * time.Second
	const drainTimeout = 2 * time.Second

	// Use unique port to avoid conflicts
	listenPort := getAvailablePort(t, 9200)
	started := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(sleepDuration)
		w.WriteHeader(http.StatusOK)
	}

	// Create the server manually to have more control
	route, err := NewRoute("v1", "/long", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, drainTimeout, hConfig)
	}

	server, err := NewRunner(WithContext(context.Background()), WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Channel to capture Run's completion
	done := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		err := server.Run(context.Background())
		done <- err
	}()

	// Wait for the server to enter the Running state
	waitForRunningState(t, server, 2*time.Second)

	// Make a request to trigger the handler
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://localhost%s/long", listenPort))
		if err == nil {
			assert.NoError(t, resp.Body.Close())
		}
	}()

	// Wait for handler to start, then initiate shutdown
	select {
	case <-started:
		server.Stop()
	case <-time.After(1 * time.Second):
		t.Fatal("Handler did not start in time")
	}

	// Measure shutdown time
	start := time.Now()
	err = <-done
	elapsed := time.Since(start)

	// Verify shutdown behavior with timeout error
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrGracefulShutdownTimeout), "Expected shutdown timeout error")
	require.GreaterOrEqual(t, elapsed, drainTimeout, "Shutdown didn't wait the minimum time")
	require.LessOrEqual(t, elapsed, drainTimeout+500*time.Millisecond, "Shutdown took too long")
}

// TestString verifies the correct string representation of the Runner
func TestString(t *testing.T) {
	t.Parallel()

	t.Run("with config", func(t *testing.T) {
		// Use a unique port
		listenPort := getAvailablePort(t, 8700)
		handler := func(w http.ResponseWriter, r *http.Request) {}
		route, err := NewRoute("v1", "/", handler)
		require.NoError(t, err)
		hConfig := Routes{*route}

		// Create the Config and HTTPServer instance
		cfgCallback := func() (*Config, error) {
			return NewConfig(listenPort, 0, hConfig)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		server, err := NewRunner(WithContext(ctx), WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Test string representation before starting
		expectedStr := fmt.Sprintf("HTTPServer{listening: %s}", listenPort)
		assert.Equal(t, expectedStr, server.String(), "Wrong string representation before running")

		// Start the server
		done := make(chan error, 1)
		go func() {
			err := server.Run(context.Background())
			done <- err
		}()

		// Wait for the server to start
		waitForRunningState(t, server, 2*time.Second)

		// Test string representation while running
		assert.Equal(t, expectedStr, server.String(), "Wrong string representation while running")

		// Clean up
		server.Stop()
		<-done
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
		assert.Equal(t, "HTTPServer<nil>", stub.String())
	})
}

// TestServerErr verifies behavior when a server fails due to address in use
func TestServerErr(t *testing.T) {
	t.Parallel()

	// Use fixed port for this test as we intentionally want a conflict
	port := ":9230"

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("v1", "/", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	// Create two server configs using the same port
	cfg1 := func() (*Config, error) { return NewConfig(port, 0, hConfig) }
	server1, err := NewRunner(WithContext(context.Background()), WithConfigCallback(cfg1))
	require.NoError(t, err)

	// Start the first server
	done1 := make(chan error, 1)
	go func() {
		err := server1.Run(context.Background())
		done1 <- err
	}()

	// Give time for the first server to start
	waitForRunningState(t, server1, 2*time.Second)

	// Create a second server with the same port
	cfg2 := func() (*Config, error) { return NewConfig(port, 0, hConfig) }
	server2, err := NewRunner(WithContext(context.Background()), WithConfigCallback(cfg2))
	require.NoError(t, err)

	// The second server should fail to start with "address already in use"
	err = server2.Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "address already in use")

	// Clean up
	server1.Stop()
	<-done1
}

// TestRoutesRequired verifies that configs must include routes
func TestRoutesRequired(t *testing.T) {
	c, err := NewConfig(":8080", 0, Routes{})
	assert.Nil(t, c)
	assert.Error(t, err)
}

// TestBootFailure tests various boot failure scenarios
func TestBootFailure(t *testing.T) {
	t.Parallel()

	t.Run("Config callback returns nil", func(t *testing.T) {
		t.Parallel()

		callback := func() (*Config, error) { return nil, nil }
		runner, err := NewRunner(
			WithContext(context.Background()),
			WithConfigCallback(callback),
		)

		assert.Error(t, err)
		assert.Nil(t, runner)
		assert.Contains(t, err.Error(), "failed to load initial config")
	})

	t.Run("Config callback returns error", func(t *testing.T) {
		t.Parallel()

		callback := func() (*Config, error) { return nil, errors.New("failed to load config") }
		runner, err := NewRunner(
			WithContext(context.Background()),
			WithConfigCallback(callback),
		)

		assert.Error(t, err)
		assert.Nil(t, runner)
		assert.Contains(t, err.Error(), "failed to load initial config")
	})

	t.Run("Server boot fails with invalid port", func(t *testing.T) {
		t.Parallel()

		handler := func(w http.ResponseWriter, r *http.Request) {}
		route, err := NewRoute("v1", "/", handler)
		require.NoError(t, err)

		callback := func() (*Config, error) {
			return &Config{
				ListenAddr:   "invalid-port",
				DrainTimeout: 1 * time.Second,
				Routes:       Routes{*route},
			}, nil
		}

		runner, err := NewRunner(
			WithContext(context.Background()),
			WithConfigCallback(callback),
		)

		assert.NoError(t, err)
		assert.NotNil(t, runner)

		// Test actual run
		err = runner.Run(context.Background())
		assert.Error(t, err)
		assert.True(t, errors.Is(err, ErrHttpServer), "Expected error to be ErrHttpServer")
		assert.Equal(t, finitestate.StatusError, runner.GetState())
	})
}

// TestStopServerWhenNotRunning verifies behavior when stopping a non-running server
func TestStopServerWhenNotRunning(t *testing.T) {
	t.Parallel()

	server, _, _ := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)

	err := server.stopServer(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server not running")
}

// Define a custom type for context keys to avoid string collision
type contextKey string

// TestWithContext verifies the WithContext option works correctly
func TestWithContext(t *testing.T) {
	t.Parallel()

	// Create a custom context with a value using the type-safe key
	testKey := contextKey("test-key")
	customCtx := context.WithValue(context.Background(), testKey, "test-value")

	// Create a server with the custom context
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("v1", "/", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(":0", 1*time.Second, hConfig)
	}

	server, err := NewRunner(WithContext(context.Background()),
		WithConfigCallback(cfgCallback),
		WithContext(customCtx))
	require.NoError(t, err)

	// Verify the custom context was applied
	actualValue := server.ctx.Value(testKey)
	assert.Equal(t, "test-value", actualValue, "Context value should be preserved")

	// Verify cancellation works through server.Stop()
	done := make(chan struct{})
	go func() {
		<-server.ctx.Done()
		close(done)
	}()

	// Call Stop to cancel the internal context
	server.Stop()

	// Wait for the server context to be canceled or timeout
	select {
	case <-done:
		// Success, context was canceled
	case <-time.After(1 * time.Second):
		t.Fatal("Context cancellation not propagated")
	}
}

// TestServerLifecycle tests the complete lifecycle of the server
func TestServerLifecycle(t *testing.T) {
	t.Parallel()

	// Use unique port numbers for parallel tests
	listenPort := getAvailablePort(t, 8800)
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("v1", "/", handler)
	require.NoError(t, err)

	// Create the Config and HTTPServer instance
	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, 1*time.Second, Routes{*route})
	}

	server, err := NewRunner(WithContext(context.Background()), WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Run the server in a goroutine
	done := make(chan error, 1)
	go func() {
		err := server.Run(context.Background())
		done <- err
	}()

	// Wait for the server to start
	waitForRunningState(t, server, 2*time.Second)

	// Test stop and state transition
	server.Stop()
	err = <-done

	assert.NoError(t, err, "Server should shut down without error")
	assert.Equal(
		t,
		finitestate.StatusStopped,
		server.GetState(),
		"Server should be in stopped state",
	)
}
