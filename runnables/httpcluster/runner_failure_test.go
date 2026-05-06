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
	"github.com/robbyt/go-supervisor/internal/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// createCrashingMockServer returns a mock that reports Running until crashCh is
// closed or ctx is done, then has Run() return crashErr. The cluster's child
// goroutine distinguishes a real crash (its server-ctx still live) from a
// shutdown-driven exit (ctx cancelled), so always returning the same error
// is safe — the cleanup-path return is filtered out by `c.Err() != nil`.
func createCrashingMockServer(
	ctx context.Context,
	crashCh <-chan struct{},
	crashErr error,
) *mocks.MockRunnableWithStateable {
	mockServer := mocks.NewMockRunnableWithStateable()
	mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		select {
		case <-ctx.Done():
		case <-crashCh:
		}
	}).Return(crashErr)
	mockServer.On("Stop").Return().Maybe()
	mockServer.On("GetState").Return(finitestate.StatusRunning).Maybe()
	mockServer.On("IsReady").Return(true).Maybe()
	stateChan := make(chan string, 1)
	stateChan <- finitestate.StatusRunning
	mockServer.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()
	return mockServer
}

// createErroredMockServer returns a mock whose GetState reports Error so
// waitForReady short-circuits via its `s == "Error"` branch. Used to
// simulate a child that fails to reach Running during a config update.
func createErroredMockServer(ctx context.Context) *mocks.MockRunnableWithStateable {
	mockServer := mocks.NewMockRunnableWithStateable()
	mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-ctx.Done()
	}).Return(nil)
	mockServer.On("Stop").Return().Maybe()
	mockServer.On("GetState").Return(finitestate.StatusError).Maybe()
	mockServer.On("IsReady").Return(false).Maybe()
	stateChan := make(chan string, 1)
	stateChan <- finitestate.StatusError
	mockServer.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()
	return mockServer
}

// TestRunner_ChildRuntimeError_TransitionsToError covers Bug A: a child
// server's Run() returning an error during steady-state must transition the
// cluster to StatusError and drop the dead entry. Cluster Run() must not exit;
// healthy siblings must keep running.
func TestRunner_ChildRuntimeError_TransitionsToError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	crashErr := errors.New("simulated server crash")
	crashCh := make(chan struct{})

	var (
		mu              sync.Mutex
		survivor        *mocks.MockRunnableWithStateable
		failingServerID = "failing"
	)

	mockFactory := func(_ context.Context, id string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		if id == failingServerID {
			return createCrashingMockServer(ctx, crashCh, crashErr), nil
		}
		mu.Lock()
		defer mu.Unlock()
		survivor = createMockServer(ctx)
		return survivor, nil
	}

	cluster, err := NewRunner(WithRunnerFactory(mockFactory))
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() {
		runErr <- cluster.Run(ctx)
	}()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	cluster.configSiphon <- map[string]*httpserver.Config{
		failingServerID: createTestHTTPConfig(t, ":18001"),
		"survivor":      createTestHTTPConfig(t, ":18002"),
	}
	require.Eventually(t, func() bool { return cluster.GetServerCount() == 2 },
		2*time.Second, 10*time.Millisecond)

	close(crashCh)

	require.Eventually(t, func() bool {
		return cluster.GetState() == finitestate.StatusError &&
			cluster.GetServerCount() == 1
	}, 2*time.Second, 10*time.Millisecond,
		"cluster must enter Error and drop the dead entry")

	select {
	case err := <-runErr:
		t.Fatalf("cluster Run() returned unexpectedly: %v", err)
	case <-time.After(50 * time.Millisecond):
		// Run must keep going — one bad child shouldn't kill the cluster.
	}

	mu.Lock()
	require.NotNil(t, survivor, "survivor mock should have been created")
	mu.Unlock()

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}

// TestRunner_ConfigUpdateFailure_TransitionsToError covers Bug B: when a
// config update spawns a server that fails readiness, the cluster must
// transition to StatusError instead of silently transitioning back to Running.
func TestRunner_ConfigUpdateFailure_TransitionsToError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	mockFactory := func(serverCtx context.Context, _ string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		return createErroredMockServer(serverCtx), nil
	}

	cluster, err := NewRunner(WithRunnerFactory(mockFactory))
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() {
		runErr <- cluster.Run(ctx)
	}()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	cluster.configSiphon <- map[string]*httpserver.Config{
		"broken": createTestHTTPConfig(t, ":18101"),
	}

	require.Eventually(t, func() bool {
		return cluster.GetState() == finitestate.StatusError &&
			cluster.GetServerCount() == 0
	}, 2*time.Second, 10*time.Millisecond,
		"cluster must enter Error after a failed config-update server")

	select {
	case err := <-runErr:
		t.Fatalf("cluster Run() returned unexpectedly: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}

// TestRunner_PartialFailure_GoodSiblingsSurvive ensures one failing server in
// a config update transitions the cluster to Error but the surviving server
// stays running and accounted for.
func TestRunner_PartialFailure_GoodSiblingsSurvive(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	const brokenID = "broken"

	var (
		mu       sync.Mutex
		survivor *mocks.MockRunnableWithStateable
	)

	mockFactory := func(serverCtx context.Context, id string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		if id == brokenID {
			return createErroredMockServer(serverCtx), nil
		}
		mu.Lock()
		defer mu.Unlock()
		survivor = createMockServer(serverCtx)
		return survivor, nil
	}

	cluster, err := NewRunner(WithRunnerFactory(mockFactory))
	require.NoError(t, err)

	runErr := make(chan error, 1)
	go func() {
		runErr <- cluster.Run(ctx)
	}()
	require.Eventually(t, cluster.IsReady, time.Second, 5*time.Millisecond)

	cluster.configSiphon <- map[string]*httpserver.Config{
		brokenID:   createTestHTTPConfig(t, ":18201"),
		"survivor": createTestHTTPConfig(t, ":18202"),
	}

	require.Eventually(t, func() bool {
		return cluster.GetState() == finitestate.StatusError &&
			cluster.GetServerCount() == 1
	}, 2*time.Second, 10*time.Millisecond,
		"cluster must enter Error and keep the survivor")

	mu.Lock()
	require.NotNil(t, survivor, "survivor mock must have been created")
	mu.Unlock()

	select {
	case err := <-runErr:
		t.Fatalf("cluster Run() returned unexpectedly: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("cluster did not shut down after ctx cancel")
	}
}

// TestRemoveEntryIfMatches covers the per-server crash handler's identity
// guard. The fix keeps an old goroutine's late error path from removing a
// healthy replacement entry that landed under the same id during a restart.
func TestRemoveEntryIfMatches(t *testing.T) {
	t.Parallel()

	makeEntries := func(t *testing.T, id string, runner httpServerRunner) *entries {
		t.Helper()
		return &entries{
			servers: map[string]*serverEntry{
				id: {id: id, runner: runner},
			},
		}
	}

	t.Run("removes when runner matches", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		mockServer := mocks.NewMockRunnableWithStateable()
		runner.currentEntries = makeEntries(t, "x", mockServer)

		runner.removeEntryIfMatches("x", mockServer)

		require.Equal(t, 0, runner.currentEntries.count(),
			"entry should be removed when runner matches")
	})

	t.Run("keeps entry when runner differs (replacement race)", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		oldServer := mocks.NewMockRunnableWithStateable()
		newServer := mocks.NewMockRunnableWithStateable()
		runner.currentEntries = makeEntries(t, "x", newServer)

		// Old goroutine fires its crash handler after the entry has been
		// replaced — the stale id reference must not delete the new entry.
		runner.removeEntryIfMatches("x", oldServer)

		require.Equal(t, 1, runner.currentEntries.count(),
			"replacement entry must not be dropped by old runner's crash path")
		got := runner.currentEntries.get("x")
		require.NotNil(t, got)
		require.Same(t, newServer, got.runner)
	})

	t.Run("no-op when id absent", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)
		mockServer := mocks.NewMockRunnableWithStateable()

		// Does not panic; entries collection unchanged.
		runner.removeEntryIfMatches("missing", mockServer)
		require.Equal(t, 0, runner.currentEntries.count())
	})
}
