package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWaitForEvent_IgnoresStaleInstanceError is a regression test for a bug
// where a late error from a superseded server instance (the old server after
// a reload) would crash the healthy current runner. The fix makes each
// serverInstance own its error channel, and waitForEvent re-loads the current
// instance's channel on every loop iteration — so once a reload has swapped in
// a new instance, an error arriving on the *old* instance's channel is never
// observed.
//
// To exercise the real swap path (per review feedback), this drives an actual
// reload: the old instance is live first, a reload swaps in a new instance,
// and only then is a late error injected on the OLD instance's channel. A
// non-faithful version that simply sends on a never-current channel would pass
// trivially without proving the swap behavior.
func TestWaitForEvent_IgnoresStaleInstanceError(t *testing.T) {
	// Not parallel: this drives two real server boots (initial + reload). Under
	// the race detector on a loaded CI runner the boot readiness probes are
	// timing-sensitive, so we avoid competing for CPU/ports with the package's
	// other parallel real-network tests.

	// Each callback invocation returns a config on a fresh port with a
	// different IdleTimeout, so the reload is a real config change that boots a
	// new server instance rather than being skipped as a no-op.
	var version int
	cfgCallback := func() (*Config, error) {
		version++
		addr := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		return createReloadTestConfigWithIdle(t, addr, "/", time.Second,
			time.Minute+time.Duration(version)*time.Millisecond), nil
	}

	server, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		server.Stop()
		<-done
	})

	require.Eventually(t, func() bool {
		return server.GetState() == finitestate.StatusRunning
	}, 10*time.Second, 10*time.Millisecond)

	// Capture the live (soon-to-be-superseded) instance.
	oldInst := server.instance.Load()
	require.NotNil(t, oldInst)
	require.NotNil(t, oldInst.errCh)

	// Reload swaps in a new instance; waitForEvent re-loops onto its channel.
	require.NoError(t, server.Reload(ctx))
	require.Eventually(t, func() bool {
		newInst := server.instance.Load()
		return newInst != nil && newInst != oldInst &&
			server.GetState() == finitestate.StatusRunning
	}, 10*time.Second, 10*time.Millisecond, "reload should swap in a new instance and stay Running")

	// Late error from the now-superseded instance, on its own buffered channel.
	// If waitForEvent were still listening on the old channel, this would crash
	// the runner into Error.
	oldInst.errCh <- assert.AnError

	// The stale error must NOT crash the healthy runner.
	require.Never(t, func() bool {
		return server.GetState() == finitestate.StatusError
	}, 250*time.Millisecond, 25*time.Millisecond,
		"a superseded instance's late error must be ignored")
	require.Equal(t, finitestate.StatusRunning, server.GetState())
}

// createReloadTestConfigWithIdle is like createReloadTestConfig but lets the
// caller set IdleTimeout so successive reloads produce distinct configs.
func createReloadTestConfigWithIdle(
	t *testing.T,
	addr, path string,
	drainTimeout, idleTimeout time.Duration,
) *Config {
	t.Helper()
	route, err := NewRouteFromHandlerFunc("test", path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	require.NoError(t, err)
	cfg, err := NewConfig(addr, Routes{*route},
		WithDrainTimeout(drainTimeout), WithIdleTimeout(idleTimeout))
	require.NoError(t, err)
	return cfg
}
