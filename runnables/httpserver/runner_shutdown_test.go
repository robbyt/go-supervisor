package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopServerFailures(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	t.Run("shutdown_timeout", func(t *testing.T) {
		mockServer := &mockHttpServer{
			shutdownFunc: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}

		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		callback := func() (*Config, error) {
			return NewConfig(port, Routes{*route}, WithDrainTimeout(10*time.Millisecond))
		}

		runner, err := NewRunner(WithConfigCallback(callback))
		require.NoError(t, err)

		runner.server = mockServer

		err = runner.stopServer(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrGracefulShutdownTimeout)
	})

	t.Run("shutdown_error", func(t *testing.T) {
		customErr := errors.New("custom shutdown error")
		mockServer := &mockHttpServer{
			shutdownErr: customErr,
		}

		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		callback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(callback))
		require.NoError(t, err)

		runner.server = mockServer

		err = runner.stopServer(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrGracefulShutdown)
		assert.Contains(t, err.Error(), customErr.Error())
	})

	t.Run("config_nil_uses_default_timeout", func(t *testing.T) {
		mockServer := &mockHttpServer{}

		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		callback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(callback))
		require.NoError(t, err)

		runner.server = mockServer

		// Clear config to test default drain timeout path
		runner.config.Store(nil)

		err = runner.stopServer(context.Background())
		require.NoError(t, err)
	})
}

type mockHttpServer struct {
	listenAndServeErr error
	shutdownErr       error
	shutdownFunc      func(ctx context.Context) error
}

func (m *mockHttpServer) ListenAndServe() error {
	if m.listenAndServeErr != nil {
		return m.listenAndServeErr
	}
	select {}
}

func (m *mockHttpServer) Shutdown(ctx context.Context) error {
	if m.shutdownFunc != nil {
		return m.shutdownFunc(ctx)
	}
	return m.shutdownErr
}

// TestShutdownFSMTransitions tests the FSM state transitions during shutdown
func TestShutdownFSMTransitions(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	t.Run("successful_fsm_transitions", func(t *testing.T) {
		mockServer := &mockHttpServer{}

		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		cfgCallback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Set FSM to Running state (normally done by Run method)
		err = runner.fsm.Transition("Booting")
		require.NoError(t, err)
		err = runner.fsm.Transition("Running")
		require.NoError(t, err)

		runner.server = mockServer

		// Call shutdown and verify state transitions
		err = runner.shutdown(context.Background())
		require.NoError(t, err)

		// Verify final state is Stopped
		assert.Equal(t, "Stopped", runner.fsm.GetState())
	})

	t.Run("stopserver_error_sets_error_state", func(t *testing.T) {
		customErr := errors.New("stopserver failure")
		mockServer := &mockHttpServer{
			shutdownErr: customErr,
		}

		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		cfgCallback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Set FSM to Running state
		err = runner.fsm.Transition("Booting")
		require.NoError(t, err)
		err = runner.fsm.Transition("Running")
		require.NoError(t, err)

		runner.server = mockServer

		// Call shutdown and expect error
		err = runner.shutdown(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrGracefulShutdown)

		// Verify error state is set
		assert.Equal(t, "Error", runner.fsm.GetState())
	})

	t.Run("duplicate_stopping_transition_succeeds", func(t *testing.T) {
		mockServer := &mockHttpServer{}

		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		cfgCallback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Set FSM to Running state
		err = runner.fsm.Transition("Booting")
		require.NoError(t, err)
		err = runner.fsm.Transition("Running")
		require.NoError(t, err)

		runner.server = mockServer

		// Force FSM into Stopping state, then call shutdown which will try to transition to Stopping again
		// This should cause the first transition to fail, but continue
		err = runner.fsm.Transition("Stopping")
		require.NoError(t, err)

		// Call shutdown - initial FSM transition will fail but shutdown should continue
		err = runner.shutdown(context.Background())
		require.NoError(t, err)

		// Verify final state is Stopped
		assert.Equal(t, "Stopped", runner.fsm.GetState())
	})

	t.Run("initial_fsm_transition_error_continues_shutdown", func(t *testing.T) {
		mockServer := &mockHttpServer{}

		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		cfgCallback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Start in Error state to cause initial transition to fail
		err = runner.fsm.Transition("Error")
		require.NoError(t, err)

		runner.server = mockServer

		// Call shutdown - initial FSM transition will fail but shutdown should continue
		err = runner.shutdown(context.Background())
		require.NoError(t, err)

		// Verify final state is Stopped despite initial transition failure
		assert.Equal(t, "Stopped", runner.fsm.GetState())
	})

	t.Run("shutdown_when_server_nil", func(t *testing.T) {
		port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		cfgCallback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Don't set a server (leave it nil)
		// Set FSM to Running state
		err = runner.fsm.Transition("Booting")
		require.NoError(t, err)
		err = runner.fsm.Transition("Running")
		require.NoError(t, err)

		// Call shutdown with nil server
		err = runner.shutdown(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrServerNotRunning)

		// Verify error state is set
		assert.Equal(t, "Error", runner.fsm.GetState())
	})
}

// mockCountingServer counts the number of times Shutdown is called
type mockCountingServer struct {
	mockHttpServer
	shutdownCount int
	mutex         sync.Mutex
}

func (m *mockCountingServer) Shutdown(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.shutdownCount++
	return m.shutdownErr
}

// TestServerCleanupOnlyOnce tests that the server shutdown is only done once
// even when stopServer is called multiple times.
func TestServerCleanupOnlyOnce(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	// Create a mock server that counts shutdown calls
	mockServer := &mockCountingServer{}

	// Set up a config callback
	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	cfgCallback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	// Create a new runner
	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Replace the server with our mock server
	runner.server = mockServer

	// Run stopServer twice concurrently to verify that sync.Once prevents
	// the actual shutdown from running more than once
	var wg sync.WaitGroup
	wg.Add(2)

	// We need a shared error slice because we'll get an error on the second call
	// when the server is already set to nil
	errorResults := make([]error, 2)

	runStopServer := func(index int) {
		defer wg.Done()
		errorResults[index] = runner.stopServer(context.Background())
	}

	go runStopServer(0)
	go runStopServer(1)

	wg.Wait()

	// Check that shutdown was only called once even though stopServer was called twice
	mockServer.mutex.Lock()
	calls := mockServer.shutdownCount
	mockServer.mutex.Unlock()
	assert.Equal(t, 1, calls, "Shutdown should only be called once")

	// With the current implementation using a separate serverMutex,
	// both calls might succeed since we set server = nil outside sync.Once
	// Verify that the shutdown was only called once
	for _, err := range errorResults {
		// Either err is nil or it's ErrServerNotRunning
		if err != nil {
			require.ErrorIs(
				t,
				err,
				ErrServerNotRunning,
				"Non-nil errors should be ErrServerNotRunning",
			)
		}
	}
}

// TestServerCleanupResetsOnRestart tests that serverCloseOnce is properly reset
// when the server is restarted, allowing it to be shut down again.
func TestStopServerResetsOnRestart(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	// Create a counting server to track shutdowns
	mockServer := &mockCountingServer{}

	// Set up a config callback
	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	cfgCallback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	// Create a new runner
	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Replace the server with our mock server
	runner.server = mockServer

	// Stop the server once
	err = runner.stopServer(context.Background())
	require.NoError(t, err)

	// Server should be nil after stopServer completes
	assert.Nil(t, runner.server)

	// Simulate a boot sequence that sets a new server and resets serverCloseOnce
	mockServer2 := &mockCountingServer{}
	runner.server = mockServer2
	runner.serverCloseOnce = sync.Once{} // This would normally be done by boot()

	// Attempt to stop the new server
	err = runner.stopServer(context.Background())
	require.NoError(t, err)

	// The server should be nil again after the second shutdown
	assert.Nil(t, runner.server)

	// Verify second server was also shut down
	mockServer2.mutex.Lock()
	calls := mockServer2.shutdownCount
	mockServer2.mutex.Unlock()
	assert.Equal(t, 1, calls, "Second server should be shut down once")
}

// TestRun_ShutdownDeadlineExceeded tests shutdown behavior when a handler exceeds the drain timeout
func TestRun_ShutdownDeadlineExceeded(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	t.Parallel()

	// Create test server with a handler that exceeds the drain timeout
	const sleepDuration = 3 * time.Second
	const drainTimeout = 1 * time.Second

	// Use unique port to avoid conflicts
	listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	started := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(sleepDuration)
		w.WriteHeader(http.StatusOK)
	}

	// Create the server manually to have more control
	route, err := NewRouteFromHandlerFunc("v1", "/long", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, hConfig, WithDrainTimeout(drainTimeout))
	}

	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Channel to capture Run's completion
	done := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		err := server.Run(t.Context())
		done <- err
	}()

	// Wait for the server to enter the Running state
	waitForState(
		t,
		server,
		finitestate.StatusRunning,
		2*time.Second,
		"Server should enter Running state",
	)

	// Make a request to trigger the handler
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://localhost%s/long", listenPort))
		if err == nil {
			assert.NoError(t, resp.Body.Close())
		}
	}()

	// Wait for handler to start, then initiate shutdown
	require.Eventually(t, func() bool {
		select {
		case <-started:
			server.Stop()
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "Handler did not start in time")

	// Measure shutdown time
	start := time.Now()
	err = <-done
	elapsed := time.Since(start)

	// Verify shutdown behavior with timeout error
	require.Error(t, err)
	require.ErrorIs(t, err, ErrGracefulShutdownTimeout, "Expected shutdown timeout error")
	require.GreaterOrEqual(t, elapsed, drainTimeout, "Shutdown didn't wait the minimum time")
	require.LessOrEqual(t, elapsed, drainTimeout+500*time.Millisecond, "Shutdown took too long")
}

// TestRun_ShutdownWithDrainTimeout tests that the server waits for handlers to complete
// within the drain timeout period
func TestRun_ShutdownWithDrainTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	t.Parallel()

	// Create test server with a handler that sleeps
	const sleepDuration = 1 * time.Second
	const drainTimeout = 3 * time.Second

	// Use unique port to avoid conflicts
	listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	started := make(chan struct{})
	handler := func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(sleepDuration)
		w.WriteHeader(http.StatusOK)
	}

	// Create the server manually
	route, err := NewRouteFromHandlerFunc("v1", "/sleep", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, hConfig, WithDrainTimeout(drainTimeout))
	}

	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Channel to capture Run's completion
	done := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		err := server.Run(t.Context())
		done <- err
	}()

	// Wait for the server to enter the Running state
	waitForState(
		t,
		server,
		finitestate.StatusRunning,
		1*time.Minute,
		"Server should enter Running state",
	)

	// Make a request to trigger the handler
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://localhost%s/sleep", listenPort))
		if err == nil {
			assert.NoError(t, resp.Body.Close())
		}
	}()

	// Wait for handler to start, then initiate shutdown
	require.Eventually(t, func() bool {
		select {
		case <-started:
			server.Stop()
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "Handler did not start in time")

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
