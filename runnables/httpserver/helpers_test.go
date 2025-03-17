package httpserver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// getAvailablePort finds and returns an available TCP port.
func getAvailablePort(t *testing.T, basePort int) string {
	t.Helper()
	for port := basePort; port <= 65535; port++ {
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			listener.Close()
			return fmt.Sprintf(":%d", port)
		}
	}
	t.FailNow()
	return ""
}

// createTestServer creates a new server for testing but doesn't start it
func createTestServer(t *testing.T, handler http.HandlerFunc, path string, drainTimeout time.Duration) (*Runner, string, chan error) {
	t.Helper()

	listenPort := getAvailablePort(t, 8000)
	route, err := NewRoute("v1", path, handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, drainTimeout, hConfig)
	}

	server, err := NewRunner(WithContext(context.Background()), WithConfigCallback(cfgCallback))
	require.NoError(t, err)
	require.NotNil(t, server)

	// Channel for Run errors
	done := make(chan error, 1)

	return server, listenPort, done
}

// setupTestServer creates a server and starts it
// nolint:unused
func setupTestServer(t *testing.T, handler http.HandlerFunc, path string, drainTimeout time.Duration) (*Runner, string, chan error) {
	t.Helper()

	server, listenPort, done := createTestServer(t, handler, path, drainTimeout)

	// Start the server in a goroutine
	go func() {
		err := server.Run(context.Background())
		done <- err
	}()

	// Give the server time to start
	time.Sleep(100 * time.Millisecond)

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

	defer resp.Body.Close()
	return resp
}
