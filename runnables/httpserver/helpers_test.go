package httpserver

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/require"
)

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
	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

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
