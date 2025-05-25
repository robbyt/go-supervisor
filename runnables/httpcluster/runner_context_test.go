package httpcluster

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestRunnerContextPersistence ensures that server contexts are not prematurely canceled
func TestRunnerContextPersistence(t *testing.T) {
	t.Parallel()

	t.Run("servers should persist after config update", func(t *testing.T) {
		// This test would have caught the bug where executeActions was canceling
		// the context immediately after starting servers
		var serverContexts []context.Context
		var mu sync.Mutex

		mockFactory := func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			mu.Lock()
			serverContexts = append(serverContexts, ctx)
			mu.Unlock()

			mockServer := mocks.NewMockRunnableWithStateable()

			// Server should run until its context is canceled
			serverRunning := make(chan struct{})
			mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				close(serverRunning)
				<-ctx.Done() // Wait for context cancellation
			}).Return(nil)

			mockServer.On("Stop").Return().Maybe()
			mockServer.On("GetState").Return(finitestate.StatusRunning)
			mockServer.On("IsRunning").Return(true)

			stateChan := make(chan string, 1)
			stateChan <- finitestate.StatusRunning
			mockServer.On("GetStateChan", mock.Anything).Return(stateChan)

			return mockServer, nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Start runner
		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner error: %v", err)
			}
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Send config to create a server
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}
		runner.configSiphon <- configs

		// Wait for server to be created
		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 1
		}, time.Second, 10*time.Millisecond)

		// Give a moment for any buggy context cancellation to occur
		time.Sleep(100 * time.Millisecond)

		// Verify the server context is still valid (not canceled)
		mu.Lock()
		require.Len(t, serverContexts, 1, "Should have created one server")
		serverCtx := serverContexts[0]
		mu.Unlock()

		select {
		case <-serverCtx.Done():
			t.Fatal("Server context was canceled prematurely - this is the bug!")
		default:
			// Good - context is still active
		}

		// Verify server is still running
		assert.Equal(t, 1, runner.GetServerCount(), "Server should still be running")

		// Send another config update
		configs["server2"] = createTestHTTPConfig(t, ":8002")
		runner.configSiphon <- configs

		// Wait for second server
		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 2
		}, time.Second, 10*time.Millisecond)

		// Original server context should still be valid
		select {
		case <-serverCtx.Done():
			t.Fatal("First server context was canceled during second config update")
		default:
			// Good - context is still active
		}
	})

	t.Run("context hierarchy is maintained correctly", func(t *testing.T) {
		// Track context relationships
		var serverCtx context.Context
		var runnerCtx context.Context

		mockFactory := func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			serverCtx = ctx

			mockServer := mocks.NewMockRunnableWithStateable()
			mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-ctx.Done()
			}).Return(nil)
			mockServer.On("Stop").Return().Maybe()
			mockServer.On("GetState").Return(finitestate.StatusRunning)
			mockServer.On("IsRunning").Return(true)

			stateChan := make(chan string, 1)
			stateChan <- finitestate.StatusRunning
			mockServer.On("GetStateChan", mock.Anything).Return(stateChan)

			return mockServer, nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		runnerCtx = ctx

		// Start runner
		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner error: %v", err)
			}
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Create a server
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}
		runner.configSiphon <- configs

		// Wait for server
		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 1
		}, time.Second, 10*time.Millisecond)

		// Verify server context is derived from runner's run context
		// (not the runner context passed to Run)
		assert.NotNil(t, serverCtx, "Server context should be set")

		// Server context should not be the same as the runner context
		// (it should be a child context for individual server lifecycle)
		assert.NotEqual(t, runnerCtx, serverCtx, "Server should have its own context")

		// When we cancel the runner context, server should eventually stop
		cancel()

		// Wait a bit for propagation
		time.Sleep(100 * time.Millisecond)

		// Server context should now be canceled
		select {
		case <-serverCtx.Done():
			// Good - context was canceled
		default:
			t.Fatal("Server context should be canceled when runner stops")
		}
	})
}
