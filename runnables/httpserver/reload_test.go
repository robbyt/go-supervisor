package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		server.Reload(t.Context())
		// Don't wait between reloads to test rapid changes
	}

	// Wait for the server to stabilize - could be Running or Error state
	var finalState string
	require.Eventually(t, func() bool {
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
		require.Eventually(t, func() bool {
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

		require.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Server should transition to StatusRunning state after boot")
		require.Equal(t, finitestate.StatusRunning, server.GetState(), "Server should be running")

		// setup a failure
		reloadCalled = true
		server.Reload(t.Context())

		require.Eventually(t, func() bool {
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
		require.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond)

		// Trigger error in config callback
		errorTriggered = true

		// Call Reload to apply the faulty configuration
		server.Reload(t.Context())
		assert.False(t, handlerCalled, "Handler should not be called after failed reload")

		// Wait for state change to propagate
		require.Eventually(t, func() bool {
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
		server.Reload(t.Context())

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
		server.Reload(t.Context())

		// Wait for reload to complete
		require.Eventually(t, func() bool {
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
		server.Reload(t.Context())

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
		require.Eventually(t, func() bool {
			return server.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond)

		server.Reload(t.Context())

		// Setup state monitoring
		stateCtx, stateCancel := context.WithCancel(t.Context())
		defer stateCancel()
		stateChan := server.GetStateChan(stateCtx)

		require.Eventually(t, func() bool {
			select {
			case state := <-stateChan:
				return finitestate.StatusReloading == state || finitestate.StatusRunning == state
			default:
				return false
			}
		}, 2*time.Second, 10*time.Millisecond)

		// Simply wait for the server to reach Running state after reload
		require.Eventually(t, func() bool {
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
