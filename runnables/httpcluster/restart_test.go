package httpcluster

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/require"
)

// TestRunner_ServerCrash_AutoRestarts verifies one_for_one supervision: when a
// single backend crashes at runtime, the cluster restarts it and stays Running
// rather than wedging the whole cluster into Error.
func TestRunner_ServerCrash_AutoRestarts(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	crashErr := errors.New("simulated crash")
	crashCh := make(chan struct{})

	var (
		mu    sync.Mutex
		calls int
	)
	// First call returns a server that becomes ready then crashes; subsequent
	// calls (the restart) return a healthy server.
	mockFactory := func(serverCtx context.Context, _ string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return createCrashingMockServer(serverCtx, crashCh, crashErr), nil
		}
		return createMockServer(serverCtx), nil
	}

	cluster, err := NewRunner(
		WithRunnerFactory(mockFactory),
		WithRestartBackoff(time.Millisecond, 5*time.Millisecond),
	)
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() { runErr <- cluster.Run(ctx) }()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	cluster.configSiphon <- map[string]*httpserver.Config{
		"web": createTestHTTPConfig(t, ":18401"),
	}
	require.Eventually(t, func() bool { return cluster.GetServerCount() == 1 },
		2*time.Second, 10*time.Millisecond)

	// Crash the server.
	close(crashCh)

	// The cluster must recreate the server (factory called again), remain
	// Running, and keep one server.
	require.Eventually(t, func() bool {
		mu.Lock()
		recreated := calls >= 2
		mu.Unlock()
		return recreated &&
			cluster.GetState() == finitestate.StatusRunning &&
			cluster.GetServerCount() == 1
	}, 2*time.Second, 10*time.Millisecond,
		"cluster should auto-restart the crashed server and stay Running")

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}

// TestRunner_ServerCrashLoop_EscalatesToError verifies the CrashLoopBackOff
// escalation: a server that keeps crashing past maxRestarts within the window
// stops being restarted and trips the whole cluster to Error.
func TestRunner_ServerCrashLoop_EscalatesToError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	crashErr := errors.New("always crashes")
	// Pre-closed: every instance becomes ready then immediately crashes.
	crashCh := make(chan struct{})
	close(crashCh)

	mockFactory := func(serverCtx context.Context, _ string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		return createCrashingMockServer(serverCtx, crashCh, crashErr), nil
	}

	cluster, err := NewRunner(
		WithRunnerFactory(mockFactory),
		WithMaxRestarts(2),
		WithRestartBackoff(time.Millisecond, 2*time.Millisecond),
		WithRestartWindow(time.Minute),
	)
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() { runErr <- cluster.Run(ctx) }()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	cluster.configSiphon <- map[string]*httpserver.Config{
		"flaky": createTestHTTPConfig(t, ":18411"),
	}

	// After more than maxRestarts crashes, the cluster escalates to Error and
	// gives up on the server.
	require.Eventually(t, func() bool {
		return cluster.GetState() == finitestate.StatusError &&
			cluster.GetServerCount() == 0
	}, 2*time.Second, 10*time.Millisecond,
		"crash loop should escalate the cluster to Error")

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}

// TestRunner_ServerCrash_SiblingsSurvive verifies one_for_one isolation: when
// one server crashes (and is restarted), an unrelated sibling keeps running and
// the cluster stays Running with both servers accounted for.
func TestRunner_ServerCrash_SiblingsSurvive(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	crashErr := errors.New("crasher down")
	crashCh := make(chan struct{})

	var (
		mu           sync.Mutex
		crasherCalls int
	)
	mockFactory := func(serverCtx context.Context, id string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		if id == "crasher" {
			mu.Lock()
			defer mu.Unlock()
			crasherCalls++
			if crasherCalls == 1 {
				return createCrashingMockServer(serverCtx, crashCh, crashErr), nil
			}
		}
		return createMockServer(serverCtx), nil
	}

	cluster, err := NewRunner(
		WithRunnerFactory(mockFactory),
		WithRestartBackoff(time.Millisecond, 5*time.Millisecond),
	)
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() { runErr <- cluster.Run(ctx) }()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	cluster.configSiphon <- map[string]*httpserver.Config{
		"crasher": createTestHTTPConfig(t, ":18421"),
		"stable":  createTestHTTPConfig(t, ":18422"),
	}
	require.Eventually(t, func() bool { return cluster.GetServerCount() == 2 },
		2*time.Second, 10*time.Millisecond)

	close(crashCh)

	// Crasher is restarted; cluster stays Running with both servers.
	require.Eventually(t, func() bool {
		mu.Lock()
		restarted := crasherCalls >= 2
		mu.Unlock()
		return restarted &&
			cluster.GetState() == finitestate.StatusRunning &&
			cluster.GetServerCount() == 2
	}, 2*time.Second, 10*time.Millisecond,
		"sibling should survive and crasher should be restarted")

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}
