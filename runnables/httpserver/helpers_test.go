package httpserver

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

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
) (*Runner, string) {
	t.Helper()

	listenPort := getAvailablePort(t, 8000)
	route, err := NewRoute("v1", path, handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, hConfig, WithDrainTimeout(drainTimeout))
	}

	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)
	require.NotNil(t, server)

	return server, listenPort
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
