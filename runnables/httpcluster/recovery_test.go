package httpcluster

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/require"
)

// TestRunner_RecoversFromErrorViaConfigUpdate verifies that a cluster wedged in
// the Error state (e.g. after a backend crash or a failed config update) can be
// recovered by pushing a new, valid config on the siphon.
//
// Before the recovery path existed, processConfigUpdate admitted updates only
// from Running, and the FSM has no Error->Running edge — so once in Error every
// subsequent update was silently dropped and the operator could never push a
// fix. This test fails against that behavior (cluster stays in Error, new
// server never starts) and passes once Error is an accepted entry state.
func TestRunner_RecoversFromErrorViaConfigUpdate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	const brokenID = "broken"

	mockFactory := func(serverCtx context.Context, id string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		if id == brokenID {
			return createErroredMockServer(serverCtx), nil
		}
		return createMockServer(serverCtx), nil
	}

	cluster, err := NewRunner(WithRunnerFactory(mockFactory))
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() { runErr <- cluster.Run(ctx) }()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	// 1. Wedge the cluster: a failing server trips it into Error.
	cluster.configSiphon <- map[string]*httpserver.Config{
		brokenID: createTestHTTPConfig(t, ":18301"),
	}
	require.Eventually(t, func() bool {
		return cluster.GetState() == finitestate.StatusError
	}, 2*time.Second, 10*time.Millisecond, "cluster should enter Error")

	// 2. Recover: a new valid config must be applied, not silently dropped.
	cluster.configSiphon <- map[string]*httpserver.Config{
		"good": createTestHTTPConfig(t, ":18302"),
	}
	require.Eventually(t, func() bool {
		return cluster.GetState() == finitestate.StatusRunning &&
			cluster.GetServerCount() == 1
	}, 2*time.Second, 10*time.Millisecond,
		"cluster should recover to Running with the new server")

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}
