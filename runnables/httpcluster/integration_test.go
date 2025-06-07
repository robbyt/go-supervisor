package httpcluster

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	route, err := httpserver.NewRouteFromHandlerFunc(
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
		networking.GetRandomListeningPort(t),
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

	// Wait for server to stop
	assert.Eventually(t, func() bool {
		return runner.GetServerCount() == 0
	}, time.Second, 10*time.Millisecond, "Server should stop")

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
	route1, err := httpserver.NewRouteFromHandlerFunc(
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

	config1, err := httpserver.NewConfig(
		networking.GetRandomListeningPort(t),
		httpserver.Routes{*route1},
	)
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
	route2, err := httpserver.NewRouteFromHandlerFunc(
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

	config2, err := httpserver.NewConfig(
		networking.GetRandomListeningPort(t),
		httpserver.Routes{*route2},
	)
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
	config3, err := httpserver.NewConfig(
		networking.GetRandomListeningPort(t),
		httpserver.Routes{*route1},
	)
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
	route, err := httpserver.NewRouteFromHandlerFunc(
		"test",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.NoError(t, err)

	config, err := httpserver.NewConfig(
		networking.GetRandomListeningPort(t),
		httpserver.Routes{*route},
	)
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

	// Wait for invalid config to be processed (should not create any servers)
	assert.Eventually(t, func() bool {
		return runner.GetServerCount() == 0 && runner.IsRunning()
	}, time.Second, 10*time.Millisecond, "Invalid config should not create servers but runner should continue")

	// Send multiple rapid config updates
	route, err := httpserver.NewRouteFromHandlerFunc(
		"test",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		// Use port 0 to let OS assign available port
		config, err := httpserver.NewConfig(
			networking.GetRandomListeningPort(t),
			httpserver.Routes{*route},
		)
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

	assert.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 10*time.Millisecond)

	// Stop runner
	cancel()

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Runner did not stop within timeout")
	}
}

func TestIntegration_IdenticalConfigPreservesServerInstance(t *testing.T) {
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

	// Create initial configuration
	route, err := httpserver.NewRouteFromHandlerFunc(
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
		networking.GetRandomListeningPort(t),
		httpserver.Routes{*route},
	)
	require.NoError(t, err)

	configs := map[string]*httpserver.Config{
		"test-server": config,
	}

	// Apply initial configuration
	select {
	case runner.configSiphon <- configs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send initial config")
	}

	// Wait for server to start
	require.Eventually(t, func() bool {
		return runner.GetServerCount() == 1
	}, time.Second, 10*time.Millisecond, "Server should start")

	// Capture reference to the original server instance
	runner.mu.RLock()
	originalEntry := runner.currentEntries.get("test-server")
	require.NotNil(t, originalEntry, "Server entry should exist")
	require.NotNil(t, originalEntry.runner, "Server runner should exist")
	originalRunner := originalEntry.runner
	runner.mu.RUnlock()

	// Wait for server to be fully ready
	require.Eventually(t, func() bool {
		return originalRunner.IsRunning()
	}, time.Second, 10*time.Millisecond, "Server should be running")

	// Apply the exact same configuration again
	select {
	case runner.configSiphon <- configs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send identical config")
	}

	// Wait for configuration processing to complete
	require.Eventually(t, func() bool {
		return runner.IsRunning() // Cluster should be back to running state
	}, time.Second, 10*time.Millisecond, "Cluster should return to running state")

	// Verify server count is still 1
	assert.Equal(t, 1, runner.GetServerCount(), "Server count should remain 1")

	// Verify the same server instance is still running
	runner.mu.RLock()
	currentEntry := runner.currentEntries.get("test-server")
	require.NotNil(t, currentEntry, "Server entry should still exist")
	require.NotNil(t, currentEntry.runner, "Server runner should still exist")
	currentRunner := currentEntry.runner
	runner.mu.RUnlock()

	// Verify the runner instance should be the same
	assert.Same(t, originalRunner, currentRunner,
		"Server instance should not have been recreated for identical config")

	// Apply the identical configuration one more time to be thorough
	select {
	case runner.configSiphon <- configs:
		// Sent successfully
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Should be able to send identical config again")
	}

	// Wait for configuration processing to complete
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 10*time.Millisecond, "Cluster should remain running")

	// Verify everything is still the same
	assert.Equal(t, 1, runner.GetServerCount(), "Server count should still be 1")

	runner.mu.RLock()
	finalEntry := runner.currentEntries.get("test-server")
	require.NotNil(t, finalEntry, "Server entry should still exist")
	require.NotNil(t, finalEntry.runner, "Server runner should still exist")
	finalRunner := finalEntry.runner
	runner.mu.RUnlock()

	assert.Same(t, originalRunner, finalRunner,
		"Server instance should still be the same after multiple identical configs")

	// Stop runner
	cancel()

	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Runner did not stop within timeout")
	}
}

func TestIntegration_IdenticalConfigDoesNotTriggerActions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// This test demonstrates the logic without the full integration complexity
	// by testing the newEntries function directly

	// Create initial configuration
	route, err := httpserver.NewRouteFromHandlerFunc(
		"test",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.NoError(t, err)

	config, err := httpserver.NewConfig(
		"127.0.0.1:0",
		httpserver.Routes{*route},
	)
	require.NoError(t, err)

	configs := map[string]*httpserver.Config{
		"test-server": config,
	}

	// Test case 1: First application should mark server for start
	entries1 := newEntries(configs)
	toStart1, toStop1 := entries1.getPendingActions()

	assert.Len(t, toStart1, 1, "First config should trigger one server start")
	assert.Len(t, toStop1, 0, "First config should not trigger any stops")
	assert.Contains(t, toStart1, "test-server", "test-server should be marked for start")

	// Simulate the server being started by updating the entry with runtime info
	entry := entries1.get("test-server")
	require.NotNil(t, entry)
	mockRunner := mocks.NewMockRunnableWithStateable()
	mockRunner.On("IsRunning").Return(true)
	mockRunner.On("GetState").Return("Running")
	entry.runner = mockRunner
	entry.action = actionNone // Clear action after "starting"

	// Create committed entries as if the first config was processed
	committedEntries := &entries{
		servers: map[string]*serverEntry{
			"test-server": {
				id:     "test-server",
				config: config,
				runner: entry.runner,
				action: actionNone,
			},
		},
	}

	// Test case 2: Applying identical config should result in no actions
	desiredEntries := newEntries(configs)
	entries2 := committedEntries.buildPendingEntries(desiredEntries).(*entries)
	toStart2, toStop2 := entries2.getPendingActions()

	assert.Len(t, toStart2, 0, "Identical config should not trigger any starts")
	assert.Len(t, toStop2, 0, "Identical config should not trigger any stops")

	// Verify the server entry action is set to actionNone
	entry2 := entries2.get("test-server")
	require.NotNil(t, entry2)
	assert.Equal(t, actionNone, entry2.action, "Server with identical config should have no action")

	// Test case 3: Applying identical config again should still result in no actions
	desiredEntries3 := newEntries(configs)
	entries3 := entries2.commit().(*entries).buildPendingEntries(desiredEntries3).(*entries)
	toStart3, toStop3 := entries3.getPendingActions()

	assert.Len(t, toStart3, 0, "Multiple identical configs should not trigger any starts")
	assert.Len(t, toStop3, 0, "Multiple identical configs should not trigger any stops")
}
