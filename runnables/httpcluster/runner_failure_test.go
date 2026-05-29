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

// TestRunner_ChildRuntimeError_IsolatedAndRestarted covers the historical
// "Bug A" scenario under the current one_for_one supervision model: a child
// server's Run() returning an error during steady-state used to wedge the
// WHOLE cluster into Error. It must now be isolated — the dead server is
// restarted (CrashLoopBackOff), the cluster stays Running, Run() does not exit,
// and healthy siblings are untouched. (Crash-loop escalation to Error is
// covered by TestRunner_ServerCrashLoop_EscalatesToError.)
func TestRunner_ChildRuntimeError_IsolatedAndRestarted(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	crashErr := errors.New("simulated server crash")
	crashCh := make(chan struct{})

	var (
		mu              sync.Mutex
		failingCalls    int
		survivor        *mocks.MockRunnableWithStateable
		failingServerID = "failing"
	)

	mockFactory := func(serverCtx context.Context, id string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
		if id == failingServerID {
			mu.Lock()
			defer mu.Unlock()
			failingCalls++
			if failingCalls == 1 {
				return createCrashingMockServer(serverCtx, crashCh, crashErr), nil
			}
			return createMockServer(serverCtx), nil
		}
		mu.Lock()
		defer mu.Unlock()
		survivor = createMockServer(serverCtx)
		return survivor, nil
	}

	cluster, err := NewRunner(
		WithRunnerFactory(mockFactory),
		WithRestartBackoff(time.Millisecond, 5*time.Millisecond),
	)
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

	// The crashed server is restarted; the cluster stays Running with both.
	require.Eventually(t, func() bool {
		mu.Lock()
		restarted := failingCalls >= 2
		mu.Unlock()
		return restarted &&
			cluster.GetState() == finitestate.StatusRunning &&
			cluster.GetServerCount() == 2
	}, 2*time.Second, 10*time.Millisecond,
		"crashed server must be restarted and cluster stay Running")

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

// The per-server crash handler's identity guard (don't drop a healthy
// replacement that landed under the same id during a restart) now lives inline
// in superviseRestart and is covered by the restart_test.go suite.
