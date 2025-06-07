package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_NoRaceCondition verifies that when IsRunning() returns true,
// the server is actually accepting TCP connections.
func TestIntegration_NoRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	const iterations = 10

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			testSingleRunnerRaceCondition(t)
		})
	}
}

func testSingleRunnerRaceCondition(t *testing.T) {
	t.Helper()

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

	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	callback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	runner, err := NewRunner(WithConfigCallback(callback))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 5*time.Second, 10*time.Millisecond, "Server should report as running")

	conn, err := net.DialTimeout("tcp", port, 100*time.Millisecond)
	assert.NoError(t, err, "TCP connection should succeed when IsRunning() returns true")

	if conn != nil {
		require.NoError(t, conn.Close())
	}

	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://" + port + "/health")
	assert.NoError(t, err, "HTTP request should succeed when IsRunning()=true")

	if resp != nil {
		assert.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer timeoutCancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-timeoutCtx.Done():
		t.Fatal("Server did not shutdown within timeout")
	}
}

// TestIntegration_FullLifecycle tests the complete lifecycle.
func TestIntegration_FullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

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

	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	callback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	runner, err := NewRunner(WithConfigCallback(callback))
	require.NoError(t, err)

	assert.Equal(t, "New", runner.GetState())
	assert.False(t, runner.IsRunning())

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	assert.Eventually(t, func() bool {
		state := runner.GetState()
		return state == "Booting" || state == "Running"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Booting")

	assert.Eventually(t, func() bool {
		return runner.IsRunning() && runner.GetState() == "Running"
	}, 5*time.Second, 50*time.Millisecond, "Should transition to Running")

	conn, err := net.DialTimeout("tcp", port, 100*time.Millisecond)
	require.NoError(t, err, "TCP should be available when Running")
	require.NoError(t, conn.Close())
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get("http://" + port + "/status")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NoError(t, resp.Body.Close())

	cancel()

	assert.Eventually(t, func() bool {
		state := runner.GetState()
		return state == "Stopping" || state == "Stopped"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Stopping")

	timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer timeoutCancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-timeoutCtx.Done():
		t.Fatal("Server did not shutdown within timeout")
	}

	assert.Eventually(t, func() bool {
		return runner.GetState() == "Stopped"
	}, 1*time.Second, 10*time.Millisecond, "Should be Stopped")

	assert.False(t, runner.IsRunning())
}
