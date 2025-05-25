package httpcluster

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getAvailablePort finds an available port by listening on :0 and returning the assigned port
func getAvailablePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() {
		if err := listener.Close(); err != nil {
			t.Logf("Failed to close listener: %v", err)
		}
	}()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)

	return "127.0.0.1:" + port
}

func TestIntegration_BasicServerLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	runner, err := NewRunner()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start runner
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	// Wait for running
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 10*time.Millisecond)

	// Add a server
	route, err := httpserver.NewRoute(
		"test",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("test response")); err != nil {
				t.Logf("Failed to write response: %v", err)
			}
		},
	)
	require.NoError(t, err)

	config, err := httpserver.NewConfig(
		getAvailablePort(t),
		httpserver.Routes{*route},
	)
	require.NoError(t, err)

	configs := map[string]*httpserver.Config{
		"test-server": config,
	}

	// Send config
	select {
	case runner.configSiphon <- configs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send config")
	}

	// Wait for server to start
	assert.Eventually(t, func() bool {
		return runner.GetServerCount() == 1
	}, 10*time.Second, 100*time.Millisecond, "Server should start")

	// Remove server
	emptyConfigs := map[string]*httpserver.Config{}
	select {
	case runner.configSiphon <- emptyConfigs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send empty config")
	}

	// Give time for server to stop
	time.Sleep(100 * time.Millisecond)

	// Stop runner
	cancel()

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Runner did not stop within timeout")
	}
}

func TestIntegration_ConfigurationChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	runner, err := NewRunner()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start runner
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	// Wait for running
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 10*time.Millisecond)

	// Create initial config
	route1, err := httpserver.NewRoute(
		"test1",
		"/test1",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("test1")); err != nil {
				t.Logf("Failed to write response: %v", err)
			}
		},
	)
	require.NoError(t, err)

	config1, err := httpserver.NewConfig(getAvailablePort(t), httpserver.Routes{*route1})
	require.NoError(t, err)

	// Send initial config
	configs := map[string]*httpserver.Config{
		"server1": config1,
	}

	select {
	case runner.configSiphon <- configs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send initial config")
	}

	// Wait for server to start
	require.Eventually(t, func() bool {
		return runner.GetServerCount() == 1
	}, time.Second, 10*time.Millisecond, "Server count should be 1")

	// Update config (same port, different route)
	route2, err := httpserver.NewRoute(
		"test2",
		"/test2",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("test2")); err != nil {
				t.Logf("Failed to write response: %v", err)
			}
		},
	)
	require.NoError(t, err)

	config2, err := httpserver.NewConfig(getAvailablePort(t), httpserver.Routes{*route2})
	require.NoError(t, err)

	updatedConfigs := map[string]*httpserver.Config{
		"server1": config2,
	}

	select {
	case runner.configSiphon <- updatedConfigs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send updated config")
	}

	// Wait for restart to complete
	require.Eventually(t, func() bool {
		return runner.GetServerCount() == 1
	}, time.Second, 10*time.Millisecond, "Server count should still be 1 after update")

	// Add second server
	config3, err := httpserver.NewConfig(getAvailablePort(t), httpserver.Routes{*route1})
	require.NoError(t, err)

	multiConfigs := map[string]*httpserver.Config{
		"server1": config2,
		"server2": config3,
	}

	select {
	case runner.configSiphon <- multiConfigs:
		// Sent successfully
	case <-time.After(10 * time.Second):
		t.Fatal("Should be able to send multi config")
	}

	// Wait for second server to start
	require.Eventually(t, func() bool {
		return runner.GetServerCount() == 2
	}, time.Second, 10*time.Millisecond, "Server count should be 2 after adding second server")

	// Stop runner
	cancel()

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Runner did not stop within timeout")
	}
}

func TestIntegration_StateReporting(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	runner, err := NewRunner()
	require.NoError(t, err)

	// Track state changes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stateChan := runner.GetStateChan(ctx)
	// Use buffered channel to collect states
	collectedStates := make(chan string, 100)

	go func() {
		for state := range stateChan {
			select {
			case collectedStates <- state:
			default:
				// Buffer full, ignore
			}
		}
		close(collectedStates)
	}()

	// Start runner
	runCtx, runCancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(runCtx)
	}()

	// Wait for running state
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 10*time.Millisecond)

	// Send config to trigger reload
	route, err := httpserver.NewRoute(
		"test",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.NoError(t, err)

	config, err := httpserver.NewConfig(getAvailablePort(t), httpserver.Routes{*route})
	require.NoError(t, err)

	configs := map[string]*httpserver.Config{
		"test-server": config,
	}

	select {
	case runner.configSiphon <- configs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send config")
	}

	// Wait for reload to complete
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 10*time.Millisecond, "Runner should be back to running after reload")

	// Stop runner
	runCancel()

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Runner did not stop within timeout")
	}

	// Cancel state collection context to stop the goroutine
	cancel()

	// Collect all states into a slice for assertions
	var states []string
	timeout := time.After(100 * time.Millisecond)
	for done := false; !done; {
		select {
		case state, ok := <-collectedStates:
			if !ok {
				done = true
			} else {
				states = append(states, state)
			}
		case <-timeout:
			done = true
		}
	}

	// Should see state transitions
	assert.Contains(t, states, "New")
	assert.Contains(t, states, "Booting")
	assert.Contains(t, states, "Running")
	assert.Contains(t, states, "Reloading") // From config update
	assert.Contains(t, states, "Stopping")
	assert.Contains(t, states, "Stopped")
}

func TestIntegration_ErrorHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	runner, err := NewRunner()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start runner
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	// Wait for running
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 10*time.Millisecond)

	// Send invalid config (this should not crash the runner)
	invalidConfigs := map[string]*httpserver.Config{
		"invalid-server": nil, // Nil config should be handled gracefully
	}

	select {
	case runner.configSiphon <- invalidConfigs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send invalid config")
	}

	// Give time for processing
	time.Sleep(100 * time.Millisecond)

	// Verify runner continues running
	assert.True(t, runner.IsRunning())

	// Send multiple rapid config updates
	route, err := httpserver.NewRoute(
		"test",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		// Use port 0 to let OS assign available port
		config, err := httpserver.NewConfig(getAvailablePort(t), httpserver.Routes{*route})
		require.NoError(t, err)

		configs := map[string]*httpserver.Config{
			fmt.Sprintf("rapid-server-%d", i): config, // Use unique IDs
		}

		select {
		case runner.configSiphon <- configs:
			// Sent successfully
		case <-time.After(10 * time.Millisecond):
			// May not all send due to processing time
			// Continue to next iteration
		}
	}

	// Give time for processing
	time.Sleep(200 * time.Millisecond)

	// Verify runner continues running
	assert.True(t, runner.IsRunning())

	// Stop runner
	cancel()

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Runner did not stop within timeout")
	}
}
