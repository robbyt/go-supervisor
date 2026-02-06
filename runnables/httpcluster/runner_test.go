package httpcluster

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockEntriesManager is a mock implementation of EntriesManager for testing
type MockEntriesManager struct {
	mock.Mock
}

func (m *MockEntriesManager) get(id string) *serverEntry {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*serverEntry)
}

func (m *MockEntriesManager) count() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockEntriesManager) countByAction(a action) int {
	args := m.Called(a)
	return args.Int(0)
}

func (m *MockEntriesManager) getPendingActions() (toStart, toStop []string) {
	args := m.Called()
	return args.Get(0).([]string), args.Get(1).([]string)
}

func (m *MockEntriesManager) commit() entriesManager {
	args := m.Called()
	return args.Get(0).(entriesManager)
}

func (m *MockEntriesManager) setRuntime(
	id string,
	runner httpServerRunner,
	ctx context.Context,
	cancel context.CancelFunc,
) entriesManager {
	args := m.Called(id, runner, ctx, cancel)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(entriesManager)
}

func (m *MockEntriesManager) clearRuntime(id string) entriesManager {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(entriesManager)
}

func (m *MockEntriesManager) buildPendingEntries(desired entriesManager) entriesManager {
	args := m.Called(desired)
	return args.Get(0).(entriesManager)
}

func (m *MockEntriesManager) removeEntry(id string) entriesManager {
	args := m.Called(id)
	return args.Get(0).(entriesManager)
}

func TestNewRunner(t *testing.T) {
	t.Parallel()
	t.Run("default options", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)
		require.NotNil(t, runner)

		assert.NotNil(t, runner.logger)
		assert.NotNil(t, runner.configSiphon)
		assert.NotNil(t, runner.currentEntries)
		assert.NotNil(t, runner.fsm)
		assert.Equal(t, finitestate.StatusNew, runner.GetState())
	})

	t.Run("with options", func(t *testing.T) {
		runner, err := NewRunner(
			WithSiphonBuffer(1),
		)
		require.NoError(t, err)

		cfg := make(map[string]*httpserver.Config)
		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer timeoutCancel()
		select {
		case runner.configSiphon <- cfg:
			// we're good!
		case <-timeoutCtx.Done():
			t.Fatal("Should be able to send config without blocking")
		}

		assert.Never(t, func() bool {
			select {
			case runner.configSiphon <- cfg:
				return true
			default:
				return false
			}
		}, 100*time.Millisecond, time.Millisecond, "Full channel should block, because Runnable hasn't booted yet")
	})

	t.Run("invalid option", func(t *testing.T) {
		invalidOption := func(r *Runner) error {
			return assert.AnError
		}

		_, err := NewRunner(invalidOption)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to apply option")
	})
}

func TestRunnerBasicInterface(t *testing.T) {
	t.Parallel()
	runner, err := NewRunner()
	require.NoError(t, err)

	t.Run("String", func(t *testing.T) {
		str := runner.String()
		assert.Contains(t, str, "HTTPCluster")
		assert.Contains(t, str, "servers=0")
		assert.Contains(t, str, "state=New")
	})

	t.Run("GetConfigSiphon", func(t *testing.T) {
		siphon := runner.GetConfigSiphon()
		assert.NotNil(t, siphon)

		// For unbuffered channel, sending would block without receiver
		// Verify that sending blocks (returns false when it can't send immediately)
		assert.Never(t, func() bool {
			select {
			case siphon <- map[string]*httpserver.Config{}:
				return true
			default:
				return false
			}
		}, 10*time.Millisecond, time.Millisecond, "Unbuffered channel should block without receiver")
	})

	t.Run("State interface", func(t *testing.T) {
		// Initial state
		assert.Equal(t, finitestate.StatusNew, runner.GetState())
		assert.False(t, runner.IsRunning())

		// GetStateChan
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		stateChan := runner.GetStateChan(ctx)
		assert.NotNil(t, stateChan)

		// Should receive current state
		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer timeoutCancel()
		select {
		case state := <-stateChan:
			assert.Equal(t, finitestate.StatusNew, state)
		case <-timeoutCtx.Done():
			t.Fatal("Should receive current state immediately")
		}
	})
}

func TestRunnerRun(t *testing.T) {
	t.Parallel()
	t.Run("basic run cycle", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		// Start runner in goroutine
		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		// Wait for runner to reach running state
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Cancel context to stop
		cancel()

		// Should stop gracefully
		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), time.Second)
		defer timeoutCancel()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-timeoutCtx.Done():
			t.Fatal("Runner did not stop within timeout")
		}

		assert.Equal(t, finitestate.StatusStopped, runner.GetState())
	})

	t.Run("stop via Stop method", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx := t.Context()

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Stop via method
		runner.Stop()

		// Should stop gracefully
		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), time.Second)
		defer timeoutCancel()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-timeoutCtx.Done():
			t.Fatal("Runner did not stop within timeout")
		}
	})

	t.Run("config siphon closed", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx := t.Context()

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Close siphon
		close(runner.configSiphon)

		// Should stop gracefully
		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), time.Second)
		defer timeoutCancel()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-timeoutCtx.Done():
			t.Fatal("Runner did not stop within timeout")
		}

		assert.Equal(t, finitestate.StatusStopped, runner.GetState())
	})
}

func TestRunnerConfigUpdate(t *testing.T) {
	t.Parallel()
	t.Run("config update when running", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		// Start runner
		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Send config update with dynamic port
		addr := networking.GetRandomListeningPort(t)
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, addr),
		}

		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer timeoutCancel()
		select {
		case runner.configSiphon <- configs:
			// Config sent
		case <-timeoutCtx.Done():
			t.Fatal("Should be able to send config")
		}

		// Verify server was created
		assert.Eventually(t, func() bool {
			return runner.GetServerCount() == 1
		}, time.Second, 10*time.Millisecond, "Server should be created")

		// Verify runner continues running
		assert.True(t, runner.IsRunning())

		cancel()
		stopCtx, stopCancel := context.WithTimeout(t.Context(), time.Second)
		defer stopCancel()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-stopCtx.Done():
			t.Fatal("Runner did not stop within timeout")
		}
	})

	t.Run("config update when not running", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Don't start runner - should ignore config updates
		addr := networking.GetRandomListeningPort(t)
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, addr),
		}

		err = runner.processConfigUpdate(t.Context(), configs)
		require.NoError(t, err) // Should not error, just ignore

		assert.Equal(t, finitestate.StatusNew, runner.GetState())
	})
}

func TestRunnerConfigUpdate_TOCTOU(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner()
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())

	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, time.Second, 5*time.Millisecond)

	addr := networking.GetRandomListeningPort(t)
	configs := map[string]*httpserver.Config{
		"server1": createTestHTTPConfig(t, addr),
	}

	// Race: call processConfigUpdate directly (bypassing the single-goroutine
	// event loop) while simultaneously stopping, to test whether the IsRunning()
	// check outside the mutex causes a problem.
	var wg sync.WaitGroup
	wg.Go(func() {
		assert.NoError(t, runner.processConfigUpdate(ctx, configs))
	})
	wg.Go(func() {
		cancel()
	})
	wg.Wait()

	require.Eventually(t, func() bool {
		select {
		case err := <-runErr:
			assert.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "runner should shut down cleanly")

	state := runner.GetState()
	assert.NotEqual(t, finitestate.StatusError, state,
		"runner should not end in Error state from concurrent config update + stop")
}

func TestRunnerStateTransitions(t *testing.T) {
	t.Parallel()
	t.Run("normal state flow", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Track state changes
		ctx := t.Context()

		stateChan := runner.GetStateChan(ctx)
		var collectedStates []string
		var mu sync.Mutex

		go func() {
			for state := range stateChan {
				mu.Lock()
				collectedStates = append(collectedStates, state)
				mu.Unlock()
			}
		}()

		// Start runner
		runCtx, runCancel := context.WithCancel(t.Context())
		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(runCtx)
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Stop
		runCancel()
		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), time.Second)
		defer timeoutCancel()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-timeoutCtx.Done():
			t.Fatal("Runner did not stop within timeout")
		}

		// Check state progression - wait for all 5 states
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(collectedStates) >= 5
		}, 2*time.Second, 10*time.Millisecond, "Should collect all 5 state transitions")

		// Should see: New -> Booting -> Running -> Stopping -> Stopped
		mu.Lock()
		defer mu.Unlock()
		assert.Contains(t, collectedStates, finitestate.StatusNew)
		assert.Contains(t, collectedStates, finitestate.StatusBooting)
		assert.Contains(t, collectedStates, finitestate.StatusRunning)
		assert.Contains(t, collectedStates, finitestate.StatusStopping)
		assert.Contains(t, collectedStates, finitestate.StatusStopped)
	})

	t.Run("error state handling", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Force FSM to error state
		runner.setStateError()

		assert.Equal(t, finitestate.StatusError, runner.GetState())
		assert.False(t, runner.IsRunning())
	})
}

func TestRunnerConcurrency(t *testing.T) {
	t.Parallel()
	t.Run("concurrent config updates", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		// Start runner
		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner returned error: %v", err)
			}
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Send multiple configs concurrently
		const numConfigs = 10
		var wg sync.WaitGroup

		testCtx, testCancel := context.WithCancel(t.Context())
		defer testCancel()

		for range numConfigs {
			wg.Go(func() {
				// Each concurrent update uses a different port
				addr := networking.GetRandomListeningPort(t)
				configs := map[string]*httpserver.Config{
					"server1": createTestHTTPConfig(t, addr),
				}

				ctx, cancel := context.WithTimeout(testCtx, 100*time.Millisecond)
				defer cancel()
				select {
				case runner.configSiphon <- configs:
					// Sent successfully
				case <-ctx.Done():
					// Timeout is ok for buffered channel
				}
			})
		}

		wg.Wait()

		// Verify runner continues running after concurrent updates
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond, "Runner did not continue running after concurrent updates")

		cancel()
	})

	t.Run("concurrent state reads", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		// Start runner
		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner returned error: %v", err)
			}
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Read state concurrently
		const numReaders = 50
		var wg sync.WaitGroup

		for range numReaders {
			wg.Go(func() {
				state := runner.GetState()
				assert.NotEmpty(t, state)

				isRunning := runner.IsRunning()
				assert.True(t, isRunning)

				str := runner.String()
				assert.Contains(t, str, "HTTPCluster")
			})
		}

		wg.Wait()
		cancel()
	})

	t.Run("concurrent stop calls", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx := t.Context()

		// Start runner
		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Call Stop concurrently
		const numStops = 10
		var wg sync.WaitGroup

		for range numStops {
			wg.Go(func() {
				runner.Stop()
			})
		}

		wg.Wait()

		// Should stop gracefully
		timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), time.Second)
		defer timeoutCancel()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-timeoutCtx.Done():
			t.Fatal("Runner did not stop within timeout")
		}
	})
}

func TestRunnerExecuteActions(t *testing.T) {
	t.Parallel()
	t.Run("execute with no actions", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Setup run context
		ctx := t.Context()

		runner.mu.Lock()
		runner.ctx = ctx
		runner.mu.Unlock()

		mockEntries := &MockEntriesManager{}
		mockEntries.On("getPendingActions").Return([]string{}, []string{})

		result := runner.executeActions(t.Context(), mockEntries)
		assert.Equal(t, mockEntries, result)

		mockEntries.AssertExpectations(t)
	})

	t.Run("execute with nil run context", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Don't set run context
		mockEntries := &MockEntriesManager{}
		mockEntries.On("getPendingActions").Return([]string{"start1"}, []string{})

		// Mock the get() method to return a test server entry
		testConfig := createTestHTTPConfig(t, ":8001")
		testEntry := &serverEntry{
			id:     "start1",
			config: testConfig,
			action: actionStart,
		}
		mockEntries.On("get", "start1").Return(testEntry)

		// Mock setRuntime to return the same mock (simulating immutable update)
		mockEntries.On("setRuntime", "start1", mock.Anything, mock.Anything, mock.Anything).
			Return(mockEntries).Maybe()

		// Mock clearRuntime in case the server fails to become ready (can happen in slow environments)
		mockEntries.On("clearRuntime", "start1").Return(mockEntries).Maybe()

		// Mock removeEntry in case the server fails to start (can happen when no run context)
		mockEntries.On("removeEntry", "start1").Return(mockEntries).Maybe()

		result := runner.executeActions(t.Context(), mockEntries)
		assert.Equal(t, mockEntries, result)

		mockEntries.AssertExpectations(t)
	})
}

func TestRunnerContextManagement(t *testing.T) {
	t.Parallel()

	t.Run("run context setup", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner returned error: %v", err)
			}
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Check run context is set
		runner.mu.RLock()
		runCtx := runner.ctx
		runCancel := runner.cancel
		runner.mu.RUnlock()

		assert.NotNil(t, runCtx)
		assert.NotNil(t, runCancel)

		cancel()
	})
}

// Test functions using the factory pattern with mocks

// createMockServer creates a mock server with standard expectations for normal operation
func createMockServer(ctx context.Context) *mocks.MockRunnableWithStateable {
	mockServer := mocks.NewMockRunnableWithStateable()

	// Standard server lifecycle expectations
	mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-ctx.Done()
	}).Return(nil)

	mockServer.On("Stop").Return()
	mockServer.On("GetState").Return(finitestate.StatusRunning).Maybe()
	mockServer.On("IsRunning").Return(true).Maybe()

	stateChan := make(chan string, 1)
	stateChan <- finitestate.StatusRunning
	mockServer.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	return mockServer
}

// createNonReadyMockServer creates a mock server that never becomes ready
func createNonReadyMockServer(ctx context.Context) *mocks.MockRunnableWithStateable {
	mockServer := mocks.NewMockRunnableWithStateable()

	mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-ctx.Done()
	}).Return(nil)

	mockServer.On("Stop").Return()
	mockServer.On("GetState").Return(finitestate.StatusNew).Maybe()
	mockServer.On("IsRunning").Return(false).Maybe()

	stateChan := make(chan string, 1)
	stateChan <- finitestate.StatusNew
	mockServer.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	return mockServer
}

func TestRunnerWithMockFactory(t *testing.T) {
	t.Parallel()

	t.Run("successful server lifecycle", func(t *testing.T) {
		var createdServers []*mocks.MockRunnableWithStateable
		var mu sync.Mutex

		mockFactory := func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			mockServer := createMockServer(ctx)

			mu.Lock()
			createdServers = append(createdServers, mockServer)
			mu.Unlock()

			return mockServer, nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Create two servers
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
			"server2": createTestHTTPConfig(t, ":8002"),
		}
		runner.configSiphon <- configs

		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 2
		}, time.Second, 10*time.Millisecond)

		mu.Lock()
		assert.Len(t, createdServers, 2)
		mu.Unlock()

		cancel()

		// Wait for Run to complete before checking expectations
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Runner did not shutdown within timeout")
		}

		for _, mock := range createdServers {
			mock.AssertExpectations(t)
		}
	})

	t.Run("server creation failure", func(t *testing.T) {
		mockFactory := func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			return nil, errors.New("server creation failed")
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}
		runner.configSiphon <- configs

		assert.Eventually(t, func() bool {
			return runner.GetServerCount() == 0
		}, time.Second, 10*time.Millisecond)

		cancel()

		// Wait for Run to complete
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Runner did not shutdown within timeout")
		}
	})

	t.Run("server readiness timeout", func(t *testing.T) {
		mockFactory := func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			return createNonReadyMockServer(ctx), nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}
		runner.configSiphon <- configs

		assert.Eventually(t, func() bool {
			return runner.GetServerCount() == 0
		}, 15*time.Second, 100*time.Millisecond)

		cancel()

		// Wait for Run to complete
		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Runner did not shutdown within timeout")
		}
	})

	t.Run("mixed success and failure", func(t *testing.T) {
		callCount := 0
		mockFactory := func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			callCount++
			if id == "server1" {
				return nil, errors.New("server1 fails")
			}
			return createMockServer(ctx), nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			err := runner.Run(ctx)
			assert.NoError(t, err)
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
			"server2": createTestHTTPConfig(t, ":8002"),
		}
		runner.configSiphon <- configs

		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 1
		}, time.Second, 10*time.Millisecond)

		cancel()
	})

	t.Run("factory parameter validation", func(t *testing.T) {
		var capturedParams struct {
			mu      sync.Mutex
			ctx     context.Context
			id      string
			config  *httpserver.Config
			handler slog.Handler
		}

		mockFactory := func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			capturedParams.mu.Lock()
			capturedParams.ctx = ctx
			capturedParams.id = id
			capturedParams.config = cfg
			capturedParams.handler = handler
			capturedParams.mu.Unlock()

			return createMockServer(ctx), nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			err := runner.Run(ctx)
			assert.NoError(t, err)
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		testConfig := createTestHTTPConfig(t, ":8001")
		configs := map[string]*httpserver.Config{
			"test-server": testConfig,
		}
		runner.configSiphon <- configs

		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 1
		}, time.Second, 10*time.Millisecond)

		capturedParams.mu.Lock()
		assert.NotNil(t, capturedParams.ctx)
		assert.Equal(t, "test-server", capturedParams.id)
		assert.Equal(t, testConfig, capturedParams.config)
		assert.NotNil(t, capturedParams.handler)
		capturedParams.mu.Unlock()

		cancel()
	})
}
