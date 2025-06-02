package httpserver

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// getAvailablePort finds an available port starting from the given base port.
// It returns a port string in the format ":port".
func getAvailablePort(t *testing.T, basePort int) string {
	t.Helper()
	for port := basePort; port < basePort+100; port++ {
		addr := net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port))
		listener, err := net.Listen("tcp", addr)
		if err == nil {
			defer func() {
				if err := listener.Close(); err != nil {
					t.Logf("Failed to close listener: %v", err)
				}
			}()
			return fmt.Sprintf(":%d", port)
		}
	}
	t.Fatalf("Could not find an available port starting from %d", basePort)
	return ""
}

// waitForState waits for the server to reach the expected state within the timeout.
func waitForState(
	t *testing.T,
	server interface{ GetState() string },
	expectedState string,
	timeout time.Duration,
	message string,
) {
	t.Helper()
	require.Eventually(t, func() bool {
		return server.GetState() == expectedState
	}, timeout, 10*time.Millisecond, message)
}

// createTestServer creates a test server with the given handler, path, and drain timeout.
// It returns a configured Runner instance and the listen address.
func createTestServer(
	t *testing.T,
	handler http.HandlerFunc,
	path string,
	drainTimeout time.Duration,
) (*Runner, string) {
	t.Helper()

	// Get an available port
	port := getAvailablePort(t, 8000)

	// Create a route
	route, err := NewRouteFromHandlerFunc("test", path, handler)
	require.NoError(t, err)
	routes := Routes{*route}

	// Create config callback
	configCallback := func() (*Config, error) {
		return NewConfig(port, routes, WithDrainTimeout(drainTimeout))
	}

	// Create the runner
	runner, err := NewRunner(WithConfigCallback(configCallback))
	require.NoError(t, err)

	return runner, port
}
