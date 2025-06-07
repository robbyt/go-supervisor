package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_NoRaceCondition verifies that when IsRunning() returns true,
// the server is actually accepting TCP connections with real FSM implementation.
func TestIntegration_NoRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	const iterations = 10 // Test multiple times to catch race conditions

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			testSingleRunnerRaceCondition(t)
		})
	}
}

func testSingleRunnerRaceCondition(t *testing.T) {
	t.Helper()
	// Create test route
	route, err := NewRouteFromHandlerFunc(
		"test",
		"/health",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("OK"))
			assert.NoError(t, err)
		},
	)
	require.NoError(t, err)

	// Get available port
	port := getAvailablePort(t, 8900)

	// Create config callback
	callback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	// Create runner with real FSM
	runner, err := NewRunner(WithConfigCallback(callback))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start runner
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	// Wait for IsRunning() to return true
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 5*time.Second, 10*time.Millisecond, "Server should report as running")

	// CRITICAL TEST: Immediately after IsRunning() = true, verify TCP connectivity
	// If there's a race condition, this should fail
	conn, err := net.DialTimeout("tcp", port, 100*time.Millisecond)
	assert.NoError(t, err,
		"RACE CONDITION DETECTED: IsRunning()=true but TCP connection failed. "+
			"This means FSM transitioned to Running before serverReadinessProbe completed.")

	if conn != nil {
		require.NoError(t, conn.Close())
	}

	// Additional verification: make an HTTP request
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://" + port + "/health")
	assert.NoError(t, err, "HTTP request should succeed when IsRunning()=true")

	if resp != nil {
		assert.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// Stop the runner
	cancel()

	// Wait for shutdown
	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Server did not shutdown within timeout")
	}
}

// TestIntegration_FullLifecycle tests the complete lifecycle with real FSM
func TestIntegration_FullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create test route
	route, err := NewRouteFromHandlerFunc(
		"lifecycle",
		"/status",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("alive"))
			assert.NoError(t, err)
		},
	)
	require.NoError(t, err)

	port := getAvailablePort(t, 8950)

	callback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	// Create runner
	runner, err := NewRunner(WithConfigCallback(callback))
	require.NoError(t, err)

	// Initial state should be New
	assert.Equal(t, "New", runner.GetState())
	assert.False(t, runner.IsRunning())

	ctx, cancel := context.WithCancel(context.Background())

	// Start runner
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	// Should transition through states: New -> Booting -> Running
	assert.Eventually(t, func() bool {
		state := runner.GetState()
		return state == "Booting" || state == "Running"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Booting")

	// Wait for Running state
	assert.Eventually(t, func() bool {
		return runner.IsRunning() && runner.GetState() == "Running"
	}, 5*time.Second, 50*time.Millisecond, "Should transition to Running")

	// Verify TCP connectivity is immediately available
	conn, err := net.DialTimeout("tcp", port, 100*time.Millisecond)
	require.NoError(t, err, "TCP should be available when Running")
	require.NoError(t, conn.Close())

	// Test HTTP endpoint
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://" + port + "/status")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NoError(t, resp.Body.Close())

	// Stop the runner
	cancel()

	// Should transition to Stopping then Stopped
	assert.Eventually(t, func() bool {
		state := runner.GetState()
		return state == "Stopping" || state == "Stopped"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Stopping")

	// Wait for final shutdown
	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Server did not shutdown within timeout")
	}

	// Final state should be Stopped
	assert.Eventually(t, func() bool {
		return runner.GetState() == "Stopped"
	}, 1*time.Second, 10*time.Millisecond, "Should be Stopped")

	assert.False(t, runner.IsRunning())
}
