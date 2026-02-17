package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConcurrentReloadsRaceCondition verifies that concurrent reloads don't cause race conditions
func TestConcurrentReloadsRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	configVersion := 0
	cfgCallback := func() (*Config, error) {
		configVersion++
		updatedCfg, err := NewConfig(
			port,
			Routes{*route},
			WithDrainTimeout(1*time.Second),
			WithIdleTimeout(time.Duration(configVersion)*time.Millisecond+1*time.Minute),
		)
		return updatedCfg, err
	}

	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	errChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		err := runner.Run(ctx)
		errChan <- err
	}()

	// Wait for server to reach Running state
	waitForState(
		t,
		runner,
		"Running",
		10*time.Second,
		"Server should reach Running state before reloads",
	)

	// Verify server is accepting connections before starting reloads
	require.Eventually(t, func() bool {
		// Check if the server is running and accepting connections
		resp, err := http.Head("http://localhost" + port + "/test")
		if err != nil {
			t.Logf("Initial connection attempt failed: %v", err)
			return false
		}
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 100*time.Millisecond, "Server should be accepting connections before reloads")

	// Launch concurrent reloads
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runner.Reload(t.Context())
		}()
	}

	// Wait for all reloads to complete
	wg.Wait()

	// Verify server is still accepting connections after reloads
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://localhost" + port + "/test")
		if err != nil {
			t.Logf("Final connection attempt failed: %v", err)
			return false
		}
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond, "Server should still be accepting connections after concurrent reloads")

	cancel()

	<-errChan
}

// TestRunnerRaceConditions verifies that there are no race conditions in the boot and stopServer methods
func TestRunnerRaceConditions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	cfg, err := NewConfig(port, Routes{*route}, WithDrainTimeout(1*time.Second))
	require.NoError(t, err)

	cfgCallback := func() (*Config, error) {
		return cfg, nil
	}

	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	errChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		err := runner.Run(ctx)
		errChan <- err
	}()

	// Wait for server to reach Running state
	waitForState(
		t,
		runner,
		"Running",
		5*time.Second,
		"Server should reach Running state before connection attempt",
	)

	// Try connecting with retries to handle potential timing issues
	require.Eventually(t, func() bool {
		resp, err := http.Get("http://localhost" + port + "/test")
		if err != nil {
			t.Logf("Connection attempt failed: %v", err)
			return false
		}
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 100*time.Millisecond, "Server should be accepting connections")

	runner.Stop()

	<-errChan
}

// TestGetConfig_ConcurrentAccess attempts to prove a race condition in getConfig()
// when multiple goroutines call it concurrently with nil config.
// Since NewRunner eagerly loads config during construction, all concurrent
// getConfig() calls find non-nil config and never hit the callback path.
func TestGetConfig_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	var callbackCount atomic.Int64

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	cfgCallback := func() (*Config, error) {
		callbackCount.Add(1)
		return NewConfig(port, Routes{*route}, WithDrainTimeout(1*time.Second))
	}

	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// NewRunner calls getConfig() once during construction
	assert.Equal(t, int64(1), callbackCount.Load())

	const goroutines = 10
	configs := make([]*Config, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			configs[i] = runner.getConfig()
		})
	}
	wg.Wait()

	// Callback should still only have been called once — config was already loaded by NewRunner
	assert.Equal(t, int64(1), callbackCount.Load())

	// All goroutines got the same config
	for i := range configs {
		assert.Same(t, configs[0], configs[i])
	}
}
