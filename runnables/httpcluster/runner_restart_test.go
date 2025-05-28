package httpcluster

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getRestartTestPort returns a dynamically allocated port for restart testing
func getRestartTestPort(tb testing.TB) string {
	tb.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)
	defer func() {
		err := listener.Close()
		require.NoError(tb, err)
	}()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(tb, err)
	return "127.0.0.1:" + port
}

// createRestartTestHTTPConfig creates a test HTTP configuration with a specific route name
func createRestartTestHTTPConfig(tb testing.TB, addr string, routeName string) *httpserver.Config {
	tb.Helper()
	route, err := httpserver.NewRoute(
		routeName,
		"/"+routeName,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, err := w.Write([]byte(routeName))
			require.NoError(tb, err)
		},
	)
	require.NoError(tb, err)

	config, err := httpserver.NewConfig(addr, httpserver.Routes{*route})
	require.NoError(tb, err)
	return config
}

// TestPortBindingRaceCondition tests the race condition scenario
// where a new server tries to bind to a port before the old one releases it
func TestPortBindingRaceCondition(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		restartDelay  time.Duration
		expectSuccess bool
	}{
		{
			name:          "zero_delay_may_fail",
			restartDelay:  0,
			expectSuccess: false, // Port binding race condition possible
		},
		{
			name:          "with_delay_should_succeed",
			restartDelay:  10 * time.Millisecond,
			expectSuccess: true,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner, err := NewRunner(WithRestartDelay(tc.restartDelay))
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				err := runner.Run(ctx)
				require.NoError(t, err)
			}()

			require.Eventually(t, func() bool {
				return runner.IsRunning()
			}, 2*time.Second, 10*time.Millisecond)

			// Use a fixed port to test rapid binding/unbinding
			port := getRestartTestPort(t)
			serverID := "test-server"

			// Create initial server
			config := createRestartTestHTTPConfig(t, port, "initial")
			runner.configSiphon <- map[string]*httpserver.Config{
				serverID: config,
			}

			// Wait for server to start
			require.Eventually(t, func() bool {
				runner.mu.RLock()
				defer runner.mu.RUnlock()
				entry := runner.currentEntries.get(serverID)
				return entry != nil && entry.runner != nil && entry.runner.IsRunning()
			}, 5*time.Second, 10*time.Millisecond)

			// Perform rapid stop/start cycles
			numCycles := 5
			successCount := 0

			for i := 0; i < numCycles; i++ {
				// Stop server by sending empty config
				runner.configSiphon <- map[string]*httpserver.Config{}

				// Wait for stop
				require.Eventually(t, func() bool {
					return runner.GetServerCount() == 0
				}, 2*time.Second, 10*time.Millisecond, "Server should stop")

				// Immediately restart with same port
				newConfig := createRestartTestHTTPConfig(t, port, fmt.Sprintf("restart-%d", i))
				runner.configSiphon <- map[string]*httpserver.Config{
					serverID: newConfig,
				}

				// Check if restart succeeded
				if assert.Eventually(t, func() bool {
					runner.mu.RLock()
					defer runner.mu.RUnlock()
					entry := runner.currentEntries.get(serverID)
					return entry != nil && entry.runner != nil && entry.runner.IsRunning()
				}, 5*time.Second, 10*time.Millisecond) {
					successCount++
				}
			}

			t.Logf(
				"Port reuse test completed with %d/%d successes (delay: %v)",
				successCount,
				numCycles,
				tc.restartDelay,
			)

			if tc.expectSuccess {
				assert.Equal(
					t,
					numCycles,
					successCount,
					"All restarts should succeed with proper delay",
				)
			} else {
				// With zero delay, we expect some failures due to port binding race
				assert.GreaterOrEqual(t, successCount, 1, "At least some restarts should succeed")
			}
		})
	}
}

// TestConcurrentServerManagement tests managing multiple servers
func TestConcurrentServerManagement(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(WithRestartDelay(10 * time.Millisecond))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		err := runner.Run(ctx)
		require.NoError(t, err)
	}()

	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 2*time.Second, 10*time.Millisecond)

	// Create multiple servers
	numServers := 3
	serverPorts := make(map[string]string)

	for i := range numServers {
		serverID := fmt.Sprintf("server%d", i)
		serverPorts[serverID] = getRestartTestPort(t)
	}

	// Test adding servers one by one
	for i := range numServers {
		config := make(map[string]*httpserver.Config)
		// Add this server while keeping previous ones
		for j := 0; j <= i; j++ {
			sid := fmt.Sprintf("server%d", j)
			config[sid] = createRestartTestHTTPConfig(
				t,
				serverPorts[sid],
				fmt.Sprintf("server-%d", j),
			)
		}

		runner.configSiphon <- config

		require.Eventually(t, func() bool {
			return runner.GetServerCount() == i+1
		}, 5*time.Second, 10*time.Millisecond, "Should have %d servers", i+1)
	}

	// Verify all servers are running
	assert.Eventually(t, func() bool {
		runner.mu.RLock()
		defer runner.mu.RUnlock()

		runningCount := 0
		for i := range numServers {
			serverID := fmt.Sprintf("server%d", i)
			entry := runner.currentEntries.get(serverID)
			if entry != nil && entry.runner != nil && entry.runner.IsRunning() {
				runningCount++
			}
		}
		return runningCount == numServers
	}, 5*time.Second, 10*time.Millisecond, "All servers should be running")

	// Test removing servers one by one
	for i := numServers - 1; i >= 0; i-- {
		config := make(map[string]*httpserver.Config)
		// Keep only servers 0 to i-1
		for j := range i {
			sid := fmt.Sprintf("server%d", j)
			config[sid] = createRestartTestHTTPConfig(
				t,
				serverPorts[sid],
				fmt.Sprintf("server-%d", j),
			)
		}

		runner.configSiphon <- config

		require.Eventually(t, func() bool {
			return runner.GetServerCount() == i
		}, 5*time.Second, 10*time.Millisecond, "Should have %d servers", i)
	}
}

// TestConcurrentRestartStress tests restart behavior under concurrent load
func TestConcurrentRestartStress(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(WithRestartDelay(10 * time.Millisecond))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		err := runner.Run(ctx)
		require.NoError(t, err)
	}()

	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 2*time.Second, 10*time.Millisecond)

	// Create two servers with different ports
	server1Port := getRestartTestPort(t)
	server2Port := getRestartTestPort(t)

	initialConfig := map[string]*httpserver.Config{
		"server1": createRestartTestHTTPConfig(t, server1Port, "server1"),
		"server2": createRestartTestHTTPConfig(t, server2Port, "server2"),
	}

	runner.configSiphon <- initialConfig

	require.Eventually(t, func() bool {
		return runner.GetServerCount() == 2
	}, 5*time.Second, 10*time.Millisecond)

	// Perform concurrent restarts on different servers
	var wg sync.WaitGroup
	restartCount := 5

	// Goroutine 1: Restart server1
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range restartCount {
			config := map[string]*httpserver.Config{
				"server1": createRestartTestHTTPConfig(
					t,
					server1Port,
					fmt.Sprintf("server1-v%d", i),
				),
				"server2": createRestartTestHTTPConfig(
					t,
					server2Port,
					"server2",
				), // Keep server2 unchanged
			}
			runner.configSiphon <- config
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// Goroutine 2: Restart server2
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range restartCount {
			config := map[string]*httpserver.Config{
				"server1": createRestartTestHTTPConfig(
					t,
					server1Port,
					"server1",
				), // Keep server1 unchanged
				"server2": createRestartTestHTTPConfig(
					t,
					server2Port,
					fmt.Sprintf("server2-v%d", i),
				),
			}
			runner.configSiphon <- config
			time.Sleep(100 * time.Millisecond)
		}
	}()

	// Wait for all restarts to complete
	wg.Wait()

	// Verify both servers are still running
	assert.Eventually(t, func() bool {
		runner.mu.RLock()
		defer runner.mu.RUnlock()

		entry1 := runner.currentEntries.get("server1")
		entry2 := runner.currentEntries.get("server2")

		return entry1 != nil && entry1.runner != nil && entry1.runner.IsRunning() &&
			entry2 != nil && entry2.runner != nil && entry2.runner.IsRunning()
	}, 5*time.Second, 10*time.Millisecond, "Both servers should be running after concurrent restarts")
}

// BenchmarkServerRestarts measures performance of server restarts
func BenchmarkServerRestarts(b *testing.B) {
	benchmarks := []struct {
		name         string
		restartDelay time.Duration
	}{
		{"NoDelay", 0},
		{"5msDelay", 5 * time.Millisecond},
		{"10msDelay", 10 * time.Millisecond},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			runner, err := NewRunner(WithRestartDelay(bm.restartDelay))
			require.NoError(b, err)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				err := runner.Run(ctx)
				require.NoError(b, err)
			}()

			// Wait for runner to start
			require.Eventually(b, func() bool {
				return runner.IsRunning()
			}, 2*time.Second, 100*time.Millisecond)

			// Create server
			port := getRestartTestPort(b)
			serverID := "bench-server"
			initialConfig := createRestartTestHTTPConfig(b, port, "bench")

			// Start server
			runner.configSiphon <- map[string]*httpserver.Config{
				serverID: initialConfig,
			}

			require.Eventually(b, func() bool {
				runner.mu.RLock()
				defer runner.mu.RUnlock()
				entry := runner.currentEntries.get(serverID)
				return entry != nil && entry.runner != nil && entry.runner.IsRunning()
			}, 5*time.Second, 100*time.Millisecond)

			b.ResetTimer()

			// Benchmark restarts
			for i := 0; i < b.N; i++ {
				newConfig := createRestartTestHTTPConfig(b, port, fmt.Sprintf("bench-%d", i))
				runner.configSiphon <- map[string]*httpserver.Config{
					serverID: newConfig,
				}

				// Wait for restart to complete
				for {
					runner.mu.RLock()
					entry := runner.currentEntries.get(serverID)
					if entry != nil && entry.runner != nil && entry.runner.IsRunning() {
						runner.mu.RUnlock()
						break
					}
					runner.mu.RUnlock()
					time.Sleep(time.Millisecond)
				}
			}

			b.StopTimer()
		})
	}
}

// BenchmarkConcurrentStateChecks benchmarks concurrent state check operations
func BenchmarkConcurrentStateChecks(b *testing.B) {
	runner, err := NewRunner(WithRestartDelay(10 * time.Millisecond))
	require.NoError(b, err)

	ctx := b.Context()

	go func() {
		err := runner.Run(ctx)
		require.NoError(b, err)
	}()

	// Wait for runner to start
	require.Eventually(b, func() bool {
		return runner.IsRunning()
	}, 2*time.Second, 10*time.Millisecond)

	// Create multiple servers
	numServers := 3
	configs := make(map[string]*httpserver.Config)

	for i := range numServers {
		serverID := fmt.Sprintf("server%d", i)
		port := getRestartTestPort(b)
		configs[serverID] = createRestartTestHTTPConfig(b, port, "bench")
	}

	// Start servers
	runner.configSiphon <- configs

	require.Eventually(b, func() bool {
		return runner.GetServerCount() == numServers
	}, 5*time.Second, 10*time.Millisecond)

	b.ResetTimer()

	// Benchmark concurrent state checks
	b.RunParallel(func(pb *testing.PB) {
		var i atomic.Int64
		i.Store(0)

		for pb.Next() {
			serverID := fmt.Sprintf("server%d", i.Load()%int64(numServers))

			// Perform state check
			runner.mu.RLock()
			entry := runner.currentEntries.get(serverID)
			if entry != nil && entry.runner != nil {
				_ = entry.runner.GetState()
			}
			runner.mu.RUnlock()

			i.Add(1)
		}
	})

	b.StopTimer()
}
