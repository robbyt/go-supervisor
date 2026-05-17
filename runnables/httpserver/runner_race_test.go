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
			// Each Reload either dispatches successfully or bails at the
			// FSM admission gate (returns nil — not a failure of *this*
			// reload). Either way no error.
			assert.NoError(t, runner.Reload(t.Context()))
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

// TestGetConfig_SerializesCallback exercises the cold path that
// TestGetConfig_ConcurrentAccess deliberately avoids: r.config is nil when
// multiple goroutines call getConfig concurrently. Without double-checked
// locking around the callback, every concurrent caller sees nil and runs
// the callback in parallel — last-write-wins on r.config and the other
// callers' configs are orphaned. With the lock, callback executions are
// serialized; once any caller stores a non-nil config, every later caller
// in the same nil-window rechecks under the lock and returns that pointer.
//
// The sleep inside the callback widens the race window so the pre-fix
// behavior (callbackCount climbs with each concurrent caller) is
// deterministic to reproduce.
func TestGetConfig_SerializesCallback(t *testing.T) {
	t.Parallel()

	var callbackCount atomic.Int64

	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	cfgCallback := func() (*Config, error) {
		// Hold the callback open long enough that every concurrent
		// caller is guaranteed to enter getConfig before the first one
		// stores a non-nil result.
		time.Sleep(50 * time.Millisecond)
		callbackCount.Add(1)
		return NewConfig(port, Routes{*route}, WithDrainTimeout(1*time.Second))
	}

	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// NewRunner eagerly loaded config once during construction.
	require.Equal(t, int64(1), callbackCount.Load())

	// Force the nil path: reset r.config so concurrent callers all see
	// nil and race the callback. Same-package test so we reach into the
	// private field directly.
	runner.config.Store(nil)

	const goroutines = 10
	configs := make([]*Config, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Go(func() {
			configs[i] = runner.getConfig()
		})
	}
	wg.Wait()

	// Without double-checked locking, every concurrent caller would
	// have run the callback in parallel (callbackCount jumps by
	// `goroutines`). With the lock, callbacks are serialized and only
	// the one that wins the race actually invokes the callback — the
	// others see the populated config when they recheck under the lock.
	assert.Equal(t, int64(2), callbackCount.Load(),
		"concurrent callback executions must be serialized to a single winner")

	// All callers must observe the same config pointer.
	for i := range configs {
		assert.Same(t, configs[0], configs[i])
	}
}
