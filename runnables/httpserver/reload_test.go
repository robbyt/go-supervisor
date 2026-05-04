package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func createReloadTestConfig(
	t *testing.T,
	addr string,
	path string,
	drainTimeout time.Duration,
) *Config {
	t.Helper()

	route, err := NewRouteFromHandlerFunc("test", path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, err)

	cfg, err := NewConfig(addr, Routes{*route}, WithDrainTimeout(drainTimeout))
	require.NoError(t, err)
	return cfg
}

// TestRapidReload tests the behavior of the server under rapid consecutive reloads
func TestRapidReload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	t.Parallel()

	// Setup initial configuration
	initialPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	route, err := NewRouteFromHandlerFunc("v1", "/", handler)
	require.NoError(t, err)
	initialRoutes := Routes{*route}

	// Track version and accumulated timeout outside the callback to maintain state
	configVersion := 0
	accumulatedTimeout := time.Duration(0) // This will track our total added milliseconds

	cfgCallback := func() (*Config, error) {
		// Increment version to track each call
		configVersion++

		// Add 1ms to our accumulated timeout
		accumulatedTimeout += time.Millisecond

		// Create a config using the constructor with the correct accumulated timeout
		// Using IdleTimeout instead of ReadTimeout as it's less likely to affect request handling
		cfg, err := NewConfig(
			initialPort,
			initialRoutes,
			WithDrainTimeout(1*time.Second),
			WithIdleTimeout(
				1*time.Minute+accumulatedTimeout,
			), // Base IdleTimeout (1m) + accumulated ms
		)
		if err != nil {
			return nil, err
		}

		return cfg, nil
	}

	// Create the Runner instance
	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)
	require.NotNil(t, server)

	// Start the server
	// Use a background context here because this test needs to control
	// the server lifecycle independent of test timeouts
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	done := make(chan error, 1)
	go func() {
		err := server.Run(serverCtx)
		done <- err
	}()
	t.Cleanup(func() {
		server.Stop()
		<-done
	})

	// Wait for server to start
	require.Eventually(t, func() bool {
		return server.GetState() == finitestate.StatusRunning
	}, 2*time.Second, 10*time.Millisecond)

	// Perform rapid reloads
	for range 10 {
		// Force config change by updating cfgCallback's closure state
		require.NoError(t, server.Reload(t.Context()))
		// Don't wait between reloads to test rapid changes
	}

	// Wait for the server to stabilize - could be Running or Error state
	var finalState string
	assert.Eventually(t, func() bool {
		finalState = server.GetState()
		return finalState == finitestate.StatusRunning || finalState == finitestate.StatusError
	}, 2*time.Second, 10*time.Millisecond)

	// Log the final state for debugging purposes
	t.Logf("Final server state after reloads: %s", finalState)

	// Skip HTTP check if server ended in Error state - it's a valid outcome of rapid reloads
	if finalState == finitestate.StatusError {
		t.Log("Server ended in Error state, which is acceptable for a rapid reload test")
		return
	}

	// Only verify HTTP response if server is in Running state
	if finalState == finitestate.StatusRunning {
		assert.Eventually(t, func() bool {
			resp, err := http.Get(fmt.Sprintf("http://localhost%s/", initialPort))
			if err != nil {
				t.Logf("HTTP request error: %v", err)
				return false
			}
			ok := resp.StatusCode == http.StatusOK
			assert.NoError(t, resp.Body.Close(), "Failed to close response body")
			return ok
		}, 2*time.Second, 100*time.Millisecond, "Server should eventually respond to HTTP requests")
	}
}

func TestReloadSkipsUnchangedConfigWithSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cfg := createReloadTestConfig(t, ":0", "/", time.Second)
		callbackCalls := 0
		runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
			callbackCalls++
			return cfg, nil
		}))
		require.NoError(t, err)
		require.Equal(t, 1, callbackCalls, "NewRunner should load the initial config")
		require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

		require.NoError(t, runner.Reload(t.Context()))

		require.Equal(t, 2, callbackCalls, "Reload should check whether the config changed")
		select {
		case req := <-runner.reloadCh:
			close(req.done)
			t.Fatal("unchanged reload should not dispatch to the Run event loop")
		default:
		}
		assert.Same(t, cfg, runner.getConfig())
	})
}

func TestReloadDispatchesChangedConfigWithSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
		updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)

		useUpdated := false
		runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
			if useUpdated {
				return updatedCfg, nil
			}
			return initialCfg, nil
		}))
		require.NoError(t, err)

		done := runner.lc.Started()
		defer done()
		require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

		useUpdated = true
		reloadDone := make(chan struct{})
		go func() {
			// assert (not require) — require.FailNow from a non-test
			// goroutine is undefined behavior per testing docs; assert
			// just records the failure.
			assert.NoError(t, runner.Reload(t.Context()))
			close(reloadDone)
		}()

		synctest.Wait()

		select {
		case <-reloadDone:
			t.Fatal("Reload should wait for the event loop to finish an accepted reload")
		default:
		}

		select {
		case req := <-runner.reloadCh:
			assert.Same(t, updatedCfg, req.cfg)
			close(req.done)
		default:
			t.Fatal("changed reload should dispatch to the Run event loop")
		}

		synctest.Wait()

		select {
		case <-reloadDone:
		default:
			t.Fatal("Reload should return once the accepted request is completed")
		}
		assert.Same(t, initialCfg, runner.getConfig(),
			"Reload should not mutate config until the event loop executes the restart")
	})
}

func TestReloadFSMAdmissionSerializesConfigCallback(t *testing.T) {
	t.Parallel()

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)

	var callbackCalls atomic.Int32
	var reloadCallbackCalls atomic.Int32
	callbackEntered := make(chan struct{})
	releaseCallback := make(chan struct{})

	runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
		if callbackCalls.Add(1) == 1 {
			return initialCfg, nil
		}

		if reloadCallbackCalls.Add(1) == 1 {
			close(callbackEntered)
		}
		<-releaseCallback
		return updatedCfg, nil
	}))
	require.NoError(t, err)
	require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

	firstReloadDone := make(chan struct{})
	go func() {
		// lc.Started was never called, so canDispatchReload returns false
		// after configCallback finally returns — the first reload ends in
		// the runner-stopped abort path. This test cares about FSM
		// admission serialization (the second Reload below), not whether
		// the first one succeeds.
		assert.Error(t, runner.Reload(t.Context()))
		close(firstReloadDone)
	}()

	assert.Eventually(t, func() bool {
		select {
		case <-callbackEntered:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(1), reloadCallbackCalls.Load())

	// Reload while FSM is in Reloading: admission gate fails → returns nil.
	require.NoError(t, runner.Reload(t.Context()))
	assert.Equal(t, int32(1), reloadCallbackCalls.Load(),
		"second reload should not run configCallback while FSM is Reloading")

	close(releaseCallback)
	assert.Eventually(t, func() bool {
		select {
		case <-firstReloadDone:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, finitestate.StatusRunning, runner.GetState())
}

func TestReloadCallerContextCanceledBeforeDispatchWithSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
		updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)

		useUpdated := false
		runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
			if useUpdated {
				return updatedCfg, nil
			}
			return initialCfg, nil
		}))
		require.NoError(t, err)

		useUpdated = true
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.ErrorIs(t, runner.Reload(ctx), context.Canceled)

		select {
		case req := <-runner.reloadCh:
			close(req.done)
			t.Fatal("pre-canceled caller context should prevent reload dispatch")
		default:
		}
		assert.Same(t, initialCfg, runner.getConfig())
	})
}

// TestReloadCanDispatchReturnsFalse_RunnerStopped covers the path where the
// FSM admission gate succeeds (FSM=Running) but canDispatchReload returns
// false because the runner has never been Started — lc.DoneCh returns the
// always-closed sentinel. Reload's post-canDispatchReload select must fire
// the lc.DoneCh case and surface the "runner stopped" error.
func TestReloadCanDispatchReturnsFalse_RunnerStopped(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
		updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)

		useUpdated := false
		runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
			if useUpdated {
				return updatedCfg, nil
			}
			return initialCfg, nil
		}))
		require.NoError(t, err)
		// FSM forced to Running so the admission gate succeeds. lc.Started
		// never called, so lc.DoneCh() returns the pre-closed sentinel
		// and canDispatchReload returns false.
		require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

		useUpdated = true
		err = runner.Reload(t.Context())
		require.Error(t, err,
			"canDispatchReload returns false → Reload returns 'runner stopped' error")
		require.Contains(t, err.Error(), "runner stopped")
		// FSM must be back in Running after the cleanup transition; not
		// stuck in Reloading and not pushed to Error.
		require.Equal(t, finitestate.StatusRunning, runner.GetState())
	})
}

func TestReloadStopsBeforeDispatchWithSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
		updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)

		useUpdated := false
		runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
			if useUpdated {
				return updatedCfg, nil
			}
			return initialCfg, nil
		}))
		require.NoError(t, err)

		useUpdated = true
		// Runner FSM is in "New" (never started); admission gate fails and
		// Reload returns nil — there's no failure of *this* reload to surface.
		// The point of this test is that no reloadReq gets dispatched.
		require.NoError(t, runner.Reload(t.Context()))

		select {
		case req := <-runner.reloadCh:
			close(req.done)
			t.Fatal("reload should not dispatch when no Run loop is active")
		default:
		}
		assert.Same(t, initialCfg, runner.getConfig())
	})
}

func TestReloadCallerContextCanceledWhileWaitingToDispatchWithSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
		updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)

		useUpdated := false
		runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
			if useUpdated {
				return updatedCfg, nil
			}
			return initialCfg, nil
		}))
		require.NoError(t, err)

		done := runner.lc.Started()
		defer done()
		require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

		blockingReq := &reloadReq{cfg: initialCfg, done: make(chan struct{})}
		runner.reloadCh <- blockingReq

		useUpdated = true
		ctx, cancel := context.WithCancel(t.Context())
		reloadDone := make(chan struct{})
		go func() {
			// Reload returns ctx.Err() when caller ctx fires while parked
			// on the reloadCh send. assert (not require) for goroutine-safety.
			assert.ErrorIs(t, runner.Reload(ctx), context.Canceled)
			close(reloadDone)
		}()

		synctest.Wait()

		select {
		case <-reloadDone:
			t.Fatal("Reload should wait while reloadCh is full")
		default:
		}

		cancel()
		synctest.Wait()

		select {
		case <-reloadDone:
		default:
			t.Fatal("Reload should return when caller context is canceled before dispatch")
		}

		select {
		case req := <-runner.reloadCh:
			assert.Same(t, blockingReq, req)
		default:
			t.Fatal("existing buffered reload request should remain queued")
		}
		assert.Same(t, initialCfg, runner.getConfig())
	})
}

func TestReloadConfigCallbackFailureSetsError(t *testing.T) {
	t.Parallel()

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	callbackErr := assert.AnError
	failReload := false
	runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
		if failReload {
			return nil, callbackErr
		}
		return initialCfg, nil
	}))
	require.NoError(t, err)
	require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

	failReload = true
	reloadErr := runner.Reload(t.Context())

	assert.Equal(t, finitestate.StatusError, runner.GetState())
	require.Error(t, reloadErr,
		"T3.1 contract: configCallback failure must surface via Reload's return")
	require.ErrorIs(t, reloadErr, callbackErr,
		"the wrapped callback error must be retrievable via errors.Is")
}

func TestReloadNilConfigSetsError(t *testing.T) {
	t.Parallel()

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	returnNil := false
	runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
		if returnNil {
			return nil, nil
		}
		return initialCfg, nil
	}))
	require.NoError(t, err)
	require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

	returnNil = true
	reloadErr := runner.Reload(t.Context())

	assert.Equal(t, finitestate.StatusError, runner.GetState())
	require.Error(t, reloadErr,
		"T3.1 contract: a nil configCallback result must surface via Reload's return")
}

func TestDrainReloadChUnblocksPendingReloadWithSynctest(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
		updatedCfg := createReloadTestConfig(t, ":0", "/", 2*time.Second)

		useUpdated := false
		runner, err := NewRunner(WithConfigCallback(func() (*Config, error) {
			if useUpdated {
				return updatedCfg, nil
			}
			return initialCfg, nil
		}))
		require.NoError(t, err)

		done := runner.lc.Started()
		defer done()
		require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))

		useUpdated = true
		reloadDone := make(chan struct{})
		go func() {
			// drainReloadCh runs below and sets req.err = "runner stopped
			// before reload was handled"; Reload surfaces that as a
			// non-nil error.
			assert.Error(t, runner.Reload(t.Context()))
			close(reloadDone)
		}()

		synctest.Wait()

		select {
		case <-reloadDone:
			t.Fatal("Reload should be waiting on the accepted request")
		default:
		}

		runner.drainReloadCh()
		synctest.Wait()

		select {
		case <-reloadDone:
		default:
			t.Fatal("drainReloadCh should close pending request done channels")
		}
	})
}

func TestExecuteReloadStopsExistingServerWithMock(t *testing.T) {
	t.Parallel()

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	runner, err := NewRunner(WithConfig(initialCfg))
	require.NoError(t, err)

	oldServer := &MockHttpServer{}
	oldServer.On("Shutdown", mock.Anything).Return(nil).Once()
	runner.server = oldServer

	updatedCfg := createReloadTestConfig(t, "invalid-port", "/", 2*time.Second)
	err = runner.executeReload(t.Context(), updatedCfg)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrServerBoot)
	oldServer.AssertExpectations(t)
	assert.Same(t, updatedCfg, runner.getConfig())
}

func TestExecuteReloadLogsFailureInsteadOfCompletion(t *testing.T) {
	t.Parallel()

	var logBuffer lockedBuffer
	logHandler := slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})

	initialCfg := createReloadTestConfig(t, ":0", "/", time.Second)
	runner, err := NewRunner(
		WithConfig(initialCfg),
		WithLogHandler(logHandler),
	)
	require.NoError(t, err)

	oldServer := &MockHttpServer{}
	oldServer.On("Shutdown", mock.Anything).Return(nil).Once()
	runner.server = oldServer

	updatedCfg := createReloadTestConfig(t, "invalid-port", "/", 2*time.Second)
	err = runner.executeReload(t.Context(), updatedCfg)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrServerBoot)
	oldServer.AssertExpectations(t)

	logs := logBuffer.String()
	assert.NotContains(t, logs, `"msg":"Completed."`)
	assert.Contains(t, logs, `"msg":"Reload failed"`)
	assert.Contains(t,
		logs, "failed to boot server during reload",
		"failure log should include the returned error: %s", logs,
	)
}

// TestReload tests the Reload method with various configurations
func TestReload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	t.Parallel()

	t.Run("Reload fails when boot fails", func(t *testing.T) {
		// Setup mock server with custom behavior
		initialPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)
		routes := Routes{*route}

		// Create normal config for initial boot
		initialConfig := &Config{
			ListenAddr:   initialPort,
			DrainTimeout: 1 * time.Second,
			Routes:       routes,
		}

		// Config callback that returns valid config initially
		// but invalid config (invalid address) on reload
		reloadCalled := false
		cfgCallback := func() (*Config, error) {
			if reloadCalled {
				// Return invalid config on reload
				return &Config{
					ListenAddr:   "invalid-port", // This will cause boot to fail
					DrainTimeout: 1 * time.Second,
					Routes:       routes,
				}, nil
			}
			return initialConfig, nil
		}

		// Create the Runner instance
		server, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)
		require.NotNil(t, server)

		// Start the server with valid config
		// Use a background context here because this test needs to control
		// the server lifecycle independent of test timeouts
		serverCtx, serverCancel := context.WithCancel(context.Background())
		defer serverCancel()

		done := make(chan error, 1)
		go func() {
			err := server.Run(serverCtx)
			done <- err
		}()

		assert.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Server should transition to StatusRunning state after boot")
		require.Equal(t, finitestate.StatusRunning, server.GetState(), "Server should be running")

		// setup a failure
		reloadCalled = true
		require.Error(t, server.Reload(t.Context()),
			"failed boot must surface as a Reload error per the T3.1 contract")

		assert.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusError
		}, 2*time.Second, 10*time.Millisecond, "Server should transition to Error state after failed boot")

		assert.Equal(t,
			finitestate.StatusError, server.GetState(),
			"Server should be in Error state",
		)

		t.Cleanup(func() {
			server.Stop()
			<-done
		})
	})

	t.Run("Reload fails when config callback returns error", func(t *testing.T) {
		// Setup initial configuration
		initialPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		handlerCalled := false
		handler := func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("initial"))
			assert.NoError(t, err)
		}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)
		routes := Routes{*route}

		// Config callback that returns an error after initial call
		errorTriggered := false
		cfgCallback := func() (*Config, error) {
			if errorTriggered {
				return nil, errors.New("config callback error")
			}
			return &Config{
				ListenAddr:   initialPort,
				DrainTimeout: 1 * time.Second,
				Routes:       routes,
			}, nil
		}

		// Create the Runner instance
		server, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)
		require.NotNil(t, server)

		// Start the server
		// Use a background context here because this test needs to control
		// the server lifecycle independent of test timeouts
		serverCtx, serverCancel := context.WithCancel(context.Background())
		defer serverCancel()

		errChan := make(chan error, 1)
		go func() {
			err := server.Run(serverCtx)
			errChan <- err
			close(errChan)
		}()

		// Wait for the server to start
		assert.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond)

		// Trigger error in config callback
		errorTriggered = true

		// Call Reload to apply the faulty configuration; the configCallback
		// failure surfaces as a Reload error per the T3.1 contract.
		require.Error(t, server.Reload(t.Context()))
		assert.False(t, handlerCalled, "Handler should not be called after failed reload")

		// Wait for state change to propagate
		assert.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusError
		}, 2*time.Second, 10*time.Millisecond)

		// Verify that the original handler still works (server doesn't stop on reload error)
		resp, err := http.Get(fmt.Sprintf("http://localhost%s/", initialPort))
		require.NoError(t, err, "After a failed reload, original server should still be running")
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		require.Equal(
			t,
			http.StatusOK,
			resp.StatusCode,
			"After a failed reload, the request should still be handled",
		)

		server.Stop()
		err = <-errChan
		require.NoError(t, err, "No error returned when stopping server")
	})

	t.Run("Reload fails when transition to Reloading state fails", func(t *testing.T) {
		// Setup initial configuration
		initialPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)
		routes := Routes{*route}

		// Create the config callback
		cfgCallback := func() (*Config, error) {
			return &Config{
				ListenAddr:   initialPort,
				DrainTimeout: 1 * time.Second,
				Routes:       routes,
			}, nil
		}

		// Create the Runner instance but don't start it - leaving it in New state
		// This will make the Reloading transition fail since it's not valid from New state
		server, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)
		require.NotNil(t, server)

		// Verify initial state
		assert.Equal(t, finitestate.StatusNew, server.GetState(), "Initial state should be New")

		// Capture server config before reload
		configBefore := server.getConfig()

		// Call Reload - should fail because transition to Reloading isn't valid from New state
		require.NoError(t, server.Reload(t.Context()))

		// Verify the state didn't change
		assert.Equal(t, finitestate.StatusNew, server.GetState(), "State should remain New")

		// Verify config didn't change
		configAfter := server.getConfig()
		assert.Same(t, configBefore, configAfter, "Config should remain unchanged")
	})

	t.Run("Reload updates server configuration", func(t *testing.T) {
		// This test is simplified to focus on the basic functionality
		// without relying on actual network connections

		// Create a test server with a handler
		initialHandler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}
		server, initialPort := createTestServer(t, initialHandler, "/", 1*time.Second)

		// Verify the initial configuration is stored
		initialCfg := server.getConfig()
		require.NotNil(t, initialCfg)
		require.Equal(t, initialPort, initialCfg.ListenAddr)

		// Create an updated configuration
		updatedPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		updatedHandler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusAccepted) // Different status code
		}
		updatedRoute, err := NewRouteFromHandlerFunc("v1", "/updated", updatedHandler)
		require.NoError(t, err)
		updatedRoutes := Routes{*updatedRoute}

		// Create a new configuration
		updatedCfg, err := NewConfig(updatedPort, updatedRoutes, WithDrainTimeout(2*time.Second))
		require.NoError(t, err)

		// Force the server to Running state so we can reload
		err = server.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// We can't easily modify the config callback directly,
		// so we'll create a new runner with the updated config
		newCfgCallback := func() (*Config, error) {
			return updatedCfg, nil
		}

		// Create a new runner with our callback
		updatedServer, err := NewRunner(
			WithConfigCallback(newCfgCallback),
		)
		require.NoError(t, err)

		// Replace the original server with our updated one for the test
		// We'll keep the original server's FSM state
		fsm := server.fsm
		server = updatedServer

		// Force the state to Running again on the new server
		err = server.fsm.SetState(fsm.GetState())
		require.NoError(t, err)

		// Call Reload to apply the new configuration
		require.NoError(t, server.Reload(t.Context()))

		// Wait for reload to complete
		assert.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond)

		// Verify the config was updated
		actualCfg := server.getConfig()
		require.Equal(t, updatedPort, actualCfg.ListenAddr)
		require.Equal(t, 2*time.Second, actualCfg.DrainTimeout)
		require.Len(t, actualCfg.Routes, 1)
		require.Equal(t, "/updated", actualCfg.Routes[0].Path)
	})

	t.Run("Reload with no prior config loads new config", func(t *testing.T) {
		// Create a config
		listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		handler := func(w http.ResponseWriter, r *http.Request) {}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)
		routes := Routes{*route}

		// First create a valid config manually
		config, err := NewConfig(listenPort, routes, WithDrainTimeout(1*time.Second))
		require.NoError(t, err)

		// Create a callback that will return our config
		cfgCallback := func() (*Config, error) {
			return config, nil
		}

		// Set up the runner with the callback
		server, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)

		// Store the initial config
		initialConfig := server.getConfig()
		require.NotNil(t, initialConfig)

		// Call Reload which should reload config
		require.NoError(t, server.Reload(t.Context()))

		// Verify the config was loaded (this doesn't test nil case, but ensures reload works)
		cfg := server.getConfig()
		require.NotNil(t, cfg)
		require.Same(t, config, cfg, "Config should be the one from callback")
	})

	t.Run("Reload with unchanged configuration should skip reload", func(t *testing.T) {
		// Setup initial configuration
		initialPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		handlerCalled := false
		handler := func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("unchanged"))
			assert.NoError(t, err)
		}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)
		routes := Routes{*route}

		// Config callback that always returns the same configuration
		currentConfig := &Config{
			ListenAddr:   initialPort,
			DrainTimeout: 1 * time.Second,
			Routes:       routes,
		}
		cfgCallback := func() (*Config, error) {
			return currentConfig, nil
		}

		// Create the Runner instance
		server, err := NewRunner(WithConfigCallback(cfgCallback))
		require.NoError(t, err)
		require.NotNil(t, server)

		// Start the server
		// Use a background context here because this test needs to control
		// the server lifecycle independent of test timeouts
		serverCtx, serverCancel := context.WithCancel(context.Background())
		defer serverCancel()

		done := make(chan error, 1)
		go func() {
			err := server.Run(serverCtx)
			done <- err
		}()
		t.Cleanup(func() {
			server.Stop()
			<-done
		})

		// Wait for the server to start
		assert.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond)

		require.NoError(t, server.Reload(t.Context()))

		// Setup state monitoring
		stateCtx, stateCancel := context.WithCancel(t.Context())
		defer stateCancel()
		stateChan := server.GetStateChan(stateCtx)

		assert.Eventually(t, func() bool {
			select {
			case state := <-stateChan:
				return finitestate.StatusReloading == state || finitestate.StatusRunning == state
			default:
				return false
			}
		}, 2*time.Second, 10*time.Millisecond)

		// Simply wait for the server to reach Running state after reload
		assert.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Server should reach Running state after reload")

		// Verify that the handler still works
		resp, err := http.Get(fmt.Sprintf("http://localhost%s/", initialPort))
		require.NoError(t, err)
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.True(t, handlerCalled, "Handler was not called")
	})
}
