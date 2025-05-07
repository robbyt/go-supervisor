package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getAvailablePort finds and returns an available TCP port.
func getAvailablePort(t *testing.T, basePort int) string {
	t.Helper()
	for port := basePort; port <= 65535; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			assert.NoError(t, listener.Close())
			return fmt.Sprintf(":%d", port)
		}
	}
	t.FailNow()
	return ""
}

// createTestServer creates a new server for testing but doesn't start it
func createTestServer(
	t *testing.T,
	handler http.HandlerFunc,
	path string,
	drainTimeout time.Duration,
) (*Runner, string, chan error) {
	t.Helper()

	listenPort := getAvailablePort(t, 8000)
	route, err := NewRoute("v1", path, handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, hConfig, WithDrainTimeout(drainTimeout))
	}

	server, err := NewRunner(WithContext(context.Background()), WithConfigCallback(cfgCallback))
	require.NoError(t, err)
	require.NotNil(t, server)

	// Channel for Run errors
	done := make(chan error, 1)

	return server, listenPort, done
}

// waitForState waits for the server to reach a specific state
// with improved diagnostics when failures occur
func waitForState(
	t *testing.T,
	server *Runner,
	targetState string,
	timeout time.Duration,
	message string,
) {
	t.Helper()

	require.Eventually(t, func() bool {
		state := server.GetState()
		t.Logf("Current state: %s, Expected: %s", state, targetState)
		return state == targetState
	}, timeout, 10*time.Millisecond, message)
}

// setupTestServer creates a server and starts it
// nolint:unused
func setupTestServer(
	t *testing.T,
	handler http.HandlerFunc,
	path string,
	drainTimeout time.Duration,
) (*Runner, string, chan error) {
	t.Helper()

	server, listenPort, done := createTestServer(t, handler, path, drainTimeout)

	// Start the server in a goroutine
	go func() {
		err := server.Run(context.Background())
		done <- err
	}()

	// Wait for the server to be ready
	waitForState(
		t,
		server,
		finitestate.StatusRunning,
		2*time.Second,
		"Server should reach Running state",
	)

	return server, listenPort, done
}

// cleanupTestServer properly cleans up a test server
func cleanupTestServer(t *testing.T, server *Runner, done chan error) {
	t.Helper()
	server.Stop()
	select {
	case <-done:
		// Server stopped
	case <-time.After(2 * time.Second):
		t.Logf("Warning: Server did not stop within timeout")
	}
}

// makeTestRequest makes an HTTP request to the given URL and returns the response
// nolint:unused
func makeTestRequest(t *testing.T, url string) *http.Response {
	t.Helper()

	resp, err := http.Get(url)
	require.NoError(t, err, "HTTP request failed")

	defer func() { assert.NoError(t, resp.Body.Close()) }()
	return resp
}
