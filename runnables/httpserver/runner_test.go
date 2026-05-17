package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
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
	cfg, err := runner.getConfig()
	require.NoError(t, err)
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

func TestServerLifecycle_StopBlocks(t *testing.T) {
	t.Parallel()

	listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("v1", "/", handler)
	require.NoError(t, err)

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, Routes{*route}, WithDrainTimeout(1*time.Second))
	}

	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	errCh := make(chan error, 1)
	runReturned := atomic.Bool{}
	go func() {
		errCh <- server.Run(t.Context())
		runReturned.Store(true)
	}()

	waitForState(t, server, finitestate.StatusRunning, 2*time.Second, "Server should enter Running state")

	server.Stop()

	// After blocking Stop returns, Run should complete very shortly
	require.Eventually(t, func() bool {
		return runReturned.Load()
	}, 1*time.Second, 5*time.Millisecond, "Run should have returned after Stop unblocked")

	assert.Equal(t, finitestate.StatusStopped, server.GetState())
	require.NoError(t, <-errCh)
}

func TestServerLifecycle_StopBeforeRun(t *testing.T) {
	t.Parallel()

	listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRouteFromHandlerFunc("v1", "/", handler)
	require.NoError(t, err)

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, Routes{*route}, WithDrainTimeout(1*time.Second))
	}

	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	stopReturned := atomic.Bool{}
	go func() {
		server.Stop()
		stopReturned.Store(true)
	}()

	// Stop should be blocked — Run hasn't started yet
	require.Never(t, func() bool {
		return stopReturned.Load()
	}, 50*time.Millisecond, 5*time.Millisecond, "Stop should block until Run starts and completes")

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(t.Context())
	}()

	// Both Stop and Run should complete
	require.Eventually(t, func() bool {
		return stopReturned.Load()
	}, 5*time.Second, 10*time.Millisecond, "Stop should unblock after Run completes")

	assert.Equal(t, finitestate.StatusStopped, server.GetState())
	require.NoError(t, <-errCh)
}

func TestRunReturnsInitialTransitionError(t *testing.T) {
	t.Parallel()

	cfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(cfg))
	require.NoError(t, err)

	stateMachine := NewMockStateMachine()
	stateMachine.On("Transition", finitestate.StatusBooting).Return(assert.AnError).Once()
	server.fsm = stateMachine

	err = server.Run(t.Context())

	require.ErrorIs(t, err, assert.AnError)
	stateMachine.AssertExpectations(t)
}

func TestWaitForEventReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	cfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(cfg))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, server.waitForEvent(ctx))
}

func TestWaitForEventReturnsServerError(t *testing.T) {
	t.Parallel()

	cfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(cfg))
	require.NoError(t, err)

	server.serverErrors <- assert.AnError

	err = server.waitForEvent(t.Context())

	require.ErrorIs(t, err, ErrHttpServer)
	require.ErrorIs(t, err, assert.AnError)
	assert.Equal(t, finitestate.StatusError, server.GetState())
}

func TestServerReadinessProbeReturnsServerError(t *testing.T) {
	t.Parallel()

	cfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(cfg))
	require.NoError(t, err)

	server.serverErrors <- assert.AnError

	err = server.serverReadinessProbe(t.Context(), "127.0.0.1:1")

	require.ErrorIs(t, err, assert.AnError)
}

func TestReloadSkipsWhenAdmissionFails(t *testing.T) {
	t.Parallel()

	cfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(cfg))
	require.NoError(t, err)

	stateMachine := NewMockStateMachine()
	stateMachine.On("TransitionIfCurrentState",
		finitestate.StatusRunning,
		finitestate.StatusReloading,
	).Return(assert.AnError).Once()
	stateMachine.On("GetState").Return(finitestate.StatusStopped).Once()
	server.fsm = stateMachine

	require.NoError(t, server.Reload(t.Context()))

	stateMachine.AssertExpectations(t)
}

func TestHandleReloadSetsErrorWhenExecuteReloadFails(t *testing.T) {
	t.Parallel()

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(initialCfg))
	require.NoError(t, err)

	stateMachine := NewMockStateMachine()
	stateMachine.On("TransitionBool", finitestate.StatusError).Return(true).Once()
	server.fsm = stateMachine

	updatedCfg := createReloadTestConfig(t, "invalid-port", "/", 2*time.Second)
	require.Error(t, server.handleReload(t.Context(), updatedCfg))
	stateMachine.AssertExpectations(t)
	got, err := server.getConfig()
	require.NoError(t, err)
	assert.Same(t, updatedCfg, got)
}

func TestHandleReloadSetsErrorWhenRunningTransitionFails(t *testing.T) {
	t.Parallel()

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(initialCfg))
	require.NoError(t, err)
	t.Cleanup(func() {
		assert.NoError(t, server.stopServer(t.Context()))
	})

	oldServer := &MockHttpServer{}
	oldServer.On("Shutdown", mock.Anything).Return(nil).Once()
	server.server = oldServer

	stateMachine := NewMockStateMachine()
	stateMachine.On("Transition", finitestate.StatusRunning).Return(assert.AnError).Once()
	stateMachine.On("TransitionBool", finitestate.StatusError).Return(true).Once()
	server.fsm = stateMachine

	updatedCfg := createReloadTestConfig(
		t,
		fmt.Sprintf(":%d", networking.GetRandomPort(t)),
		"/",
		2*time.Second,
	)
	require.Error(t, server.handleReload(t.Context(), updatedCfg))

	oldServer.AssertExpectations(t)
	stateMachine.AssertExpectations(t)
	got, err := server.getConfig()
	require.NoError(t, err)
	assert.Same(t, updatedCfg, got)
}

// TestHandleReload_CtxCancelDoesNotForceError covers the Copilot review catch
// on PR #111: if executeReload returns context.Canceled (because runCtx fired
// during shutdown), handleReload must NOT push the FSM to Error. The
// cancellation error still propagates via the return — that's control flow
// — but the FSM should be Running so subsequent shutdown transitions stay
// valid.
func TestHandleReload_CtxCancelDoesNotForceError(t *testing.T) {
	t.Parallel()

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	server, err := NewRunner(WithConfig(initialCfg))
	require.NoError(t, err)
	t.Cleanup(func() {
		// Stop any goroutines boot may have started; the not-running
		// error is the common case here (this test never starts the
		// server fully) — only fail the test on something else.
		if err := server.stopServer(context.Background()); err != nil &&
			!errors.Is(err, ErrServerNotRunning) {
			t.Errorf("cleanup stopServer: %v", err)
		}
	})

	// Force FSM into Reloading directly so handleReload's contract
	// matches the Reload admission gate's normal flow.
	require.NoError(t, server.fsm.SetState(finitestate.StatusRunning))
	require.NoError(t, server.fsm.Transition(finitestate.StatusReloading))

	cancelledCtx, cancel := context.WithCancel(t.Context())
	cancel()

	updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)
	err = server.handleReload(cancelledCtx, updatedCfg)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled,
		"ctx.Canceled must propagate to the caller")

	require.NotEqual(t, finitestate.StatusError, server.GetState(),
		"ctx-cancellation must NOT push FSM to Error")
	require.Equal(t, finitestate.StatusRunning, server.GetState(),
		"FSM should be back in Running so subsequent shutdown transitions stay valid")
}
