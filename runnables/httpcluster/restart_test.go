package httpcluster

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/mocks"
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

// TestRestartOptionValidation covers the validation branches of the
// crash-supervision options.
func TestRestartOptionValidation(t *testing.T) {
	t.Parallel()

	_, err := NewRunner(WithRestartBackoff(0, time.Second))
	require.Error(t, err, "non-positive initial backoff must be rejected")

	_, err = NewRunner(WithRestartBackoff(time.Second, time.Millisecond))
	require.Error(t, err, "max < initial must be rejected")

	_, err = NewRunner(WithMaxRestarts(-1))
	require.Error(t, err, "negative max restarts must be rejected")

	_, err = NewRunner(WithRestartWindow(0))
	require.Error(t, err, "non-positive restart window must be rejected")

	// Valid values are accepted.
	_, err = NewRunner(
		WithRestartBackoff(time.Millisecond, time.Second),
		WithMaxRestarts(3),
		WithRestartWindow(time.Minute),
	)
	require.NoError(t, err)
}

// TestBackoffForLocked covers the exponential ramp, the cap, and the
// attempt<1 clamp.
func TestBackoffForLocked(t *testing.T) {
	t.Parallel()

	r, err := NewRunner(WithRestartBackoff(10*time.Millisecond, 80*time.Millisecond))
	require.NoError(t, err)

	require.Equal(t, 10*time.Millisecond, r.backoffForLocked(0), "attempt<1 clamps to first")
	require.Equal(t, 10*time.Millisecond, r.backoffForLocked(1))
	require.Equal(t, 20*time.Millisecond, r.backoffForLocked(2))
	require.Equal(t, 40*time.Millisecond, r.backoffForLocked(3))
	require.Equal(t, 80*time.Millisecond, r.backoffForLocked(4), "capped at max")
	require.Equal(t, 80*time.Millisecond, r.backoffForLocked(10), "stays capped")
}

// TestSuperviseRestart_IgnoresSupersededRunner covers the identity guard: a
// late crash from a runner that has already been replaced under the same id
// must not record a failure or touch the live entry.
func TestSuperviseRestart_IgnoresSupersededRunner(t *testing.T) {
	t.Parallel()

	r, err := NewRunner()
	require.NoError(t, err)
	require.NoError(t, r.fsm.Transition(finitestate.StatusBooting))
	require.NoError(t, r.fsm.Transition(finitestate.StatusRunning))

	current := mocks.NewMockRunnableWithStateable()
	r.currentEntries = &entries{servers: map[string]*serverEntry{
		"x": {id: "x", config: createTestHTTPConfig(t, ":18511"), runner: current},
	}}

	stale := mocks.NewMockRunnableWithStateable()
	r.superviseRestart(context.Background(), "x", stale, errors.New("late crash"))

	require.Equal(t, finitestate.StatusRunning, r.GetState())
	require.Equal(t, 1, r.GetServerCount())
	require.Empty(t, r.restartTracker, "no failure should be recorded for a superseded runner")
	require.Same(t, current, r.currentEntries.get("x").runner, "live entry must be untouched")
}

// TestSuperviseRestart_AbortsBackoffOnShutdown covers the failure-record +
// runtime-clear + backoff-abort path when the cluster context is already done.
func TestSuperviseRestart_AbortsBackoffOnShutdown(t *testing.T) {
	t.Parallel()

	// Long backoff so the only way out of the wait is ctx cancellation.
	r, err := NewRunner(WithRestartBackoff(time.Hour, time.Hour))
	require.NoError(t, err)
	require.NoError(t, r.fsm.Transition(finitestate.StatusBooting))
	require.NoError(t, r.fsm.Transition(finitestate.StatusRunning))

	crashed := mocks.NewMockRunnableWithStateable()
	r.currentEntries = &entries{servers: map[string]*serverEntry{
		"x": {id: "x", config: createTestHTTPConfig(t, ":18512"), runner: crashed},
	}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // backoff select must take the ctx.Done branch immediately

	r.superviseRestart(ctx, "x", crashed, errors.New("boom"))

	require.Equal(t, finitestate.StatusRunning, r.GetState(), "no escalation on shutdown abort")
	require.Nil(t, r.currentEntries.get("x").runner, "dead runtime should be cleared before backoff")
	require.Len(t, r.restartTracker["x"].failures, 1, "the crash should be recorded")
}

// TestRunner_RestartFailure_EscalatesToError covers the branch where the
// restart attempt itself cannot bring the server back to ready: rather than
// spinning, the cluster escalates to Error.
func TestRunner_RestartFailure_EscalatesToError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	crashErr := errors.New("crash then refuse to start")
	crashCh := make(chan struct{})

	var (
		mu    sync.Mutex
		calls int
	)
	mockFactory := func(serverCtx context.Context, _ string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return createCrashingMockServer(serverCtx, crashCh, crashErr), nil
		}
		// The restart never becomes ready.
		return createErroredMockServer(serverCtx), nil
	}

	cluster, err := NewRunner(
		WithRunnerFactory(mockFactory),
		WithRestartBackoff(time.Millisecond, 5*time.Millisecond),
		WithMaxRestarts(5), // not a crash loop; the restart-readiness failure escalates
	)
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() { runErr <- cluster.Run(ctx) }()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	cluster.configSiphon <- map[string]*httpserver.Config{
		"web": createTestHTTPConfig(t, ":18513"),
	}
	require.Eventually(t, func() bool { return cluster.GetServerCount() == 1 },
		2*time.Second, 10*time.Millisecond)

	close(crashCh)

	require.Eventually(t, func() bool {
		return cluster.GetState() == finitestate.StatusError &&
			cluster.GetServerCount() == 0
	}, 2*time.Second, 10*time.Millisecond,
		"a restart that cannot become ready should escalate to Error")

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}
