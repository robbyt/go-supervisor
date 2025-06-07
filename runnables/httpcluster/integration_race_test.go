package httpcluster

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_HttpClusterNoRaceCondition verifies that when httpcluster
// reports a server as running, it's actually accepting TCP connections.
// This tests the real FSM implementation, not mocks.
func TestIntegration_HttpClusterNoRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	const iterations = 5

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			testHttpClusterRaceCondition(t)
		})
	}
}

func testHttpClusterRaceCondition(t *testing.T) {
	t.Helper()

	cluster, err := NewRunner()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		runErr <- cluster.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		return cluster.IsRunning()
	}, 5*time.Second, 50*time.Millisecond, "Cluster should be running")
	route, err := httpserver.NewRouteFromHandlerFunc(
		"test",
		"/health",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("healthy"))
			assert.NoError(t, err)
		},
	)
	require.NoError(t, err)

	addr := networking.GetRandomListeningPort(t)
	config, err := httpserver.NewConfig(addr, httpserver.Routes{*route})
	require.NoError(t, err)
	select {
	case cluster.GetConfigSiphon() <- map[string]*httpserver.Config{
		"test-server": config,
	}:
	case <-time.After(1 * time.Second):
		t.Fatal("Failed to send config to cluster")
	}

	require.Eventually(t, func() bool {
		return cluster.GetServerCount() == 1
	}, 10*time.Second, 100*time.Millisecond, "Server should be added to cluster")

	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	assert.NoError(t, err, "TCP connection should succeed when cluster reports server ready")

	if conn != nil {
		require.NoError(t, conn.Close())
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/health")
	assert.NoError(t, err, "HTTP request should succeed when cluster reports server ready")

	if resp != nil {
		assert.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	t.Run("server_restart_race", func(t *testing.T) {
		select {
		case cluster.GetConfigSiphon() <- map[string]*httpserver.Config{}:
		case <-time.After(1 * time.Second):
			t.Fatal("Failed to send empty config")
		}

		require.Eventually(t, func() bool {
			return cluster.GetServerCount() == 0
		}, 5*time.Second, 100*time.Millisecond, "Server should be removed")
		newAddr := networking.GetRandomListeningPort(t)
		newConfig, err := httpserver.NewConfig(newAddr, httpserver.Routes{*route})
		require.NoError(t, err)

		select {
		case cluster.GetConfigSiphon() <- map[string]*httpserver.Config{
			"test-server-restart": newConfig,
		}:
		case <-time.After(1 * time.Second):
			t.Fatal("Failed to send restart config")
		}

		require.Eventually(t, func() bool {
			return cluster.GetServerCount() == 1
		}, 10*time.Second, 100*time.Millisecond, "Server should be re-added")

		conn, err := net.DialTimeout("tcp", newAddr, 500*time.Millisecond)
		assert.NoError(t, err, "TCP connection should succeed immediately after restart")

		if conn != nil {
			require.NoError(t, conn.Close())
		}

		resp, err := client.Get("http://" + newAddr + "/health")
		assert.NoError(t, err, "HTTP should work immediately after restart")
		if resp != nil {
			assert.NoError(t, resp.Body.Close())
		}
	})

	cancel()

	timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer timeoutCancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-timeoutCtx.Done():
		t.Fatal("Cluster did not shutdown within timeout")
	}
}

// TestIntegration_HttpClusterFullLifecycle tests complete cluster lifecycle
func TestIntegration_HttpClusterFullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	cluster, err := NewRunner()
	require.NoError(t, err)

	assert.Equal(t, "New", cluster.GetState())
	assert.False(t, cluster.IsRunning())
	assert.Equal(t, 0, cluster.GetServerCount())

	ctx, cancel := context.WithCancel(t.Context())
	runErr := make(chan error, 1)
	go func() {
		runErr <- cluster.Run(ctx)
	}()

	assert.Eventually(t, func() bool {
		state := cluster.GetState()
		return state == "Booting" || state == "Running"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Booting")

	assert.Eventually(t, func() bool {
		return cluster.IsRunning() && cluster.GetState() == "Running"
	}, 5*time.Second, 50*time.Millisecond, "Should transition to Running")
	route1, err := httpserver.NewRouteFromHandlerFunc("svc1", "/svc1",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("service1"))
			assert.NoError(t, err)
		})
	require.NoError(t, err)

	route2, err := httpserver.NewRouteFromHandlerFunc("svc2", "/svc2",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte("service2"))
			assert.NoError(t, err)
		})
	require.NoError(t, err)

	addr1 := networking.GetRandomListeningPort(t)
	addr2 := networking.GetRandomListeningPort(t)

	config1, err := httpserver.NewConfig(addr1, httpserver.Routes{*route1})
	require.NoError(t, err)

	config2, err := httpserver.NewConfig(addr2, httpserver.Routes{*route2})
	require.NoError(t, err)

	select {
	case cluster.GetConfigSiphon() <- map[string]*httpserver.Config{
		"service1": config1,
		"service2": config2,
	}:
	case <-time.After(1 * time.Second):
		t.Fatal("Failed to send configs")
	}

	require.Eventually(t, func() bool {
		return cluster.GetServerCount() == 2
	}, 10*time.Second, 100*time.Millisecond, "Both servers should start")
	client := &http.Client{Timeout: 1 * time.Second}

	resp1, err := client.Get("http://" + addr1 + "/svc1")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp1.StatusCode)
	assert.NoError(t, resp1.Body.Close())

	resp2, err := client.Get("http://" + addr2 + "/svc2")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.NoError(t, resp2.Body.Close())

	select {
	case cluster.GetConfigSiphon() <- map[string]*httpserver.Config{
		"service1": config1,
	}:
	case <-time.After(1 * time.Second):
		t.Fatal("Failed to send scale-down config")
	}

	require.Eventually(t, func() bool {
		return cluster.GetServerCount() == 1
	}, 5*time.Second, 100*time.Millisecond, "Should scale down to 1 server")
	resp1, err = client.Get("http://" + addr1 + "/svc1")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp1.StatusCode)
	assert.NoError(t, resp1.Body.Close())

	resp2, err = client.Get("http://" + addr2 + "/svc2")
	if err == nil && resp2 != nil {
		assert.NoError(t, resp2.Body.Close())
	}
	assert.Error(t, err, "Service2 should be stopped")

	cancel()

	assert.Eventually(t, func() bool {
		state := cluster.GetState()
		return state == "Stopping" || state == "Stopped"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Stopping")

	timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer timeoutCancel()
	select {
	case err := <-runErr:
		assert.NoError(t, err)
	case <-timeoutCtx.Done():
		t.Fatal("Cluster did not shutdown within timeout")
	}

	assert.Eventually(t, func() bool {
		return cluster.GetState() == "Stopped"
	}, 1*time.Second, 10*time.Millisecond, "Should be Stopped")

	assert.False(t, cluster.IsRunning())
}

// TestHTTPClusterReadinessRaceCondition verifies that cluster.IsRunning() returns true
// only when servers are actually ready to accept TCP connections.
func TestHTTPClusterReadinessRaceCondition(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	port := networking.GetRandomPort(t)
	addr := "127.0.0.1:" + strconv.Itoa(port)

	cluster, err := NewRunner()
	require.NoError(t, err)

	go func() {
		if err := cluster.Run(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("Cluster run failed: %v", err)
		}
	}()

	assert.Eventually(t, func() bool {
		return cluster.IsRunning()
	}, 5*time.Second, 10*time.Millisecond, "Cluster should be running")

	route, err := httpserver.NewRouteFromHandlerFunc(
		"test",
		"/health",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("OK")); err != nil {
				return
			}
		},
	)
	require.NoError(t, err)

	config, err := httpserver.NewConfig(addr, httpserver.Routes{*route})
	require.NoError(t, err)

	select {
	case cluster.GetConfigSiphon() <- map[string]*httpserver.Config{
		"test-server": config,
	}:
	case <-ctx.Done():
		t.Fatal("Failed to send config")
	}

	assert.Eventually(t, func() bool {
		return cluster.IsRunning() && cluster.GetServerCount() == 1
	}, 10*time.Second, 50*time.Millisecond, "Server should be reported as running")

	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	assert.NoError(t, err, "TCP connection should succeed when cluster reports running")
	if conn != nil {
		assert.NoError(t, conn.Close())
	}
}
