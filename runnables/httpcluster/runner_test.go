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
	stateSub <-chan string,
) entriesManager {
	args := m.Called(id, runner, ctx, cancel, stateSub)
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

func TestNewRunner(t *testing.T) {
	t.Parallel()
	t.Run("default options", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)
		require.NotNil(t, runner)

		assert.NotNil(t, runner.logger)
		assert.NotNil(t, runner.parentCtx)
		assert.NotNil(t, runner.configSiphon)
		assert.NotNil(t, runner.currentEntries)
		assert.NotNil(t, runner.fsm)
		assert.Equal(t, finitestate.StatusNew, runner.GetState())
	})

	t.Run("with options", func(t *testing.T) {
		ctx := t.Context()
		runner, err := NewRunner(
			WithContext(ctx),
			WithSiphonBuffer(1),
		)
		require.NoError(t, err)
		assert.Equal(t, ctx, runner.parentCtx)

		cfg := make(map[string]*httpserver.Config)
		select {
		case runner.configSiphon <- cfg:
			// we're good!
		case <-time.After(100 * time.Millisecond):
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
		assert.Error(t, err)
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
		select {
		case state := <-stateChan:
			assert.Equal(t, finitestate.StatusNew, state)
		case <-ctx.Done():
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
		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(time.Second):
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
		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(time.Second):
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
		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(time.Second):
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

		// Send config update
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}

		select {
		case runner.configSiphon <- configs:
			// Config sent
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Should be able to send config")
		}

		// Verify server was created
		assert.Eventually(t, func() bool {
			return runner.GetServerCount() == 1
		}, time.Second, 10*time.Millisecond, "Server should be created")

		// Verify runner continues running
		assert.True(t, runner.IsRunning())

		cancel()
		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Runner did not stop within timeout")
		}
	})

	t.Run("config update when not running", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Don't start runner - should ignore config updates
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}

		err = runner.processConfigUpdate(context.Background(), configs)
		assert.NoError(t, err) // Should not error, just ignore

		assert.Equal(t, finitestate.StatusNew, runner.GetState())
	})
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
		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Runner did not stop within timeout")
		}

		// Check state progression
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(collectedStates) >= 4
		}, time.Second, 10*time.Millisecond)

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
		runner, err := NewRunner(WithSiphonBuffer(10))
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
		wg.Add(numConfigs)

		for i := 0; i < numConfigs; i++ {
			go func(i int) {
				defer wg.Done()
				configs := map[string]*httpserver.Config{
					"server1": createTestHTTPConfig(t, ":8001"),
				}

				select {
				case runner.configSiphon <- configs:
					// Sent successfully
				case <-time.After(100 * time.Millisecond):
					// Timeout is ok for buffered channel
				}
			}(i)
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
		wg.Add(numReaders)

		for i := 0; i < numReaders; i++ {
			go func() {
				defer wg.Done()
				state := runner.GetState()
				assert.NotEmpty(t, state)

				isRunning := runner.IsRunning()
				assert.True(t, isRunning)

				str := runner.String()
				assert.Contains(t, str, "HTTPCluster")
			}()
		}

		wg.Wait()
		cancel()
	})

	t.Run("concurrent stop calls", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx := context.Background()

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
		wg.Add(numStops)

		for i := 0; i < numStops; i++ {
			go func() {
				defer wg.Done()
				runner.Stop()
			}()
		}

		wg.Wait()

		// Should stop gracefully
		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(time.Second):
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
		runner.runCtx = ctx
		runner.mu.Unlock()

		mockEntries := &MockEntriesManager{}
		mockEntries.On("getPendingActions").Return([]string{}, []string{})

		result := runner.executeActions(context.Background(), mockEntries)
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
		mockEntries.On("setRuntime", "start1", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(mockEntries)

		result := runner.executeActions(context.Background(), mockEntries)
		assert.Equal(t, mockEntries, result)

		mockEntries.AssertExpectations(t)
	})
}

func TestRunnerContextManagement(t *testing.T) {
	t.Parallel()
	t.Run("parent context cancellation", func(t *testing.T) {
		parentCtx, parentCancel := context.WithCancel(t.Context())

		runner, err := NewRunner(WithContext(parentCtx))
		require.NoError(t, err)

		runCtx := context.Background()

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(runCtx)
		}()

		// Wait for running
		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Cancel parent context
		parentCancel()

		// Should stop gracefully
		select {
		case err := <-runErr:
			assert.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("Runner should stop when parent context cancelled")
		}
	})

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
		runCtx := runner.runCtx
		runCancel := runner.runCancel
		runner.mu.RUnlock()

		assert.NotNil(t, runCtx)
		assert.NotNil(t, runCancel)

		cancel()
	})
}

// Test functions using the factory pattern with mocks

func TestRunnerWithMockFactory(t *testing.T) {
	t.Parallel()
	t.Run("server creation with mock factory", func(t *testing.T) {
		var createdServers []*mocks.MockRunnableWithStateable
		var mu sync.Mutex

		// Create a factory that returns mocked servers
		mockFactory := func(ctx context.Context, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			mockServer := mocks.NewMockRunnableWithStateable()

			// Setup expectations
			mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				// Simulate server running
				<-ctx.Done()
			}).Return(nil)
			mockServer.On("Stop").
				Return().
				Maybe()
				// Maybe() because Stop might not be called on all servers
			mockServer.On("GetState").Return(finitestate.StatusRunning)
			mockServer.On("IsRunning").Return(true)

			// Setup state channel
			stateChan := make(chan string, 1)
			stateChan <- finitestate.StatusRunning
			mockServer.On("GetStateChan", mock.Anything).Return(stateChan)

			mu.Lock()
			createdServers = append(createdServers, mockServer)
			mu.Unlock()

			return mockServer, nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

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

		// Send config to create servers
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
			"server2": createTestHTTPConfig(t, ":8002"),
		}

		select {
		case runner.configSiphon <- configs:
			// Config sent
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Should be able to send config")
		}

		// Wait for servers to be created
		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 2
		}, time.Second, 10*time.Millisecond)

		// Verify mocks were created
		mu.Lock()
		assert.Len(t, createdServers, 2)
		mu.Unlock()

		// Verify all expectations on mocks
		for _, mock := range createdServers {
			mock.AssertExpectations(t)
		}

		cancel()
	})

	t.Run("server creation errors", func(t *testing.T) {
		failCount := 0
		mockFactory := func(ctx context.Context, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			failCount++
			if failCount == 1 {
				// First server fails
				return nil, errors.New("failed to create server")
			}
			// Second server succeeds
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

		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner error: %v", err)
			}
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Try to create 2 servers, one will fail
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
			"server2": createTestHTTPConfig(t, ":8002"),
		}

		runner.configSiphon <- configs

		// Wait for processing
		time.Sleep(200 * time.Millisecond)

		// At least one server should be created (the order is not guaranteed)
		serverCount := runner.GetServerCount()
		assert.GreaterOrEqual(t, serverCount, 1, "At least one server should be created")
		assert.LessOrEqual(t, serverCount, 2, "At most two servers should be created")

		cancel()
	})

	t.Run("server state transitions", func(t *testing.T) {
		mockFactory := func(ctx context.Context, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			mockServer := mocks.NewMockRunnableWithStateable()

			// Create a state channel we can control
			stateChan := make(chan string, 10)

			// Setup state progression
			mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				// Simulate state transitions
				stateChan <- finitestate.StatusBooting
				time.Sleep(10 * time.Millisecond)
				stateChan <- finitestate.StatusRunning

				// Wait for context cancellation
				<-ctx.Done()

				stateChan <- finitestate.StatusStopping
				time.Sleep(10 * time.Millisecond)
				stateChan <- finitestate.StatusStopped
			}).Return(nil)

			mockServer.On("Stop").Return().Maybe()
			mockServer.On("GetState").Return(finitestate.StatusRunning)
			mockServer.On("IsRunning").Return(true)
			mockServer.On("GetStateChan", mock.Anything).Return(stateChan)

			return mockServer, nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner error: %v", err)
			}
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Create a server
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}
		runner.configSiphon <- configs

		// Verify server is created
		require.Eventually(t, func() bool {
			return runner.GetServerCount() == 1
		}, time.Second, 10*time.Millisecond)

		cancel()
	})

	t.Run("server readiness timeout", func(t *testing.T) {
		mockFactory := func(ctx context.Context, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			mockServer := mocks.NewMockRunnableWithStateable()

			// Server never becomes ready (always returns New state)
			mockServer.On("GetState").Return(finitestate.StatusNew)
			mockServer.On("IsRunning").Return(false) // Not running since it's in New state
			mockServer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-ctx.Done()
			}).Return(nil)
			mockServer.On("Stop").Return().Maybe()

			stateChan := make(chan string, 1)
			stateChan <- finitestate.StatusNew
			mockServer.On("GetStateChan", mock.Anything).Return(stateChan)

			return mockServer, nil
		}

		runner, err := NewRunner(WithRunnerFactory(mockFactory))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner error: %v", err)
			}
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Try to create server that won't become ready
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}
		runner.configSiphon <- configs

		// Server should not be created due to readiness timeout
		time.Sleep(200 * time.Millisecond)
		assert.Equal(t, 0, runner.GetServerCount())

		cancel()
	})

	t.Run("factory receives correct parameters", func(t *testing.T) {
		var mu sync.Mutex
		var capturedCtx context.Context
		var capturedConfig *httpserver.Config
		var capturedHandler slog.Handler

		mockFactory := func(ctx context.Context, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error) {
			mu.Lock()
			capturedCtx = ctx
			capturedConfig = cfg
			capturedHandler = handler
			mu.Unlock()

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

		runner, err := NewRunner(
			WithRunnerFactory(mockFactory),
		)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())

		go func() {
			if err := runner.Run(ctx); err != nil {
				t.Logf("Runner error: %v", err)
			}
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, time.Second, 10*time.Millisecond)

		// Create server
		testConfig := createTestHTTPConfig(t, ":8001")
		configs := map[string]*httpserver.Config{
			"server1": testConfig,
		}
		runner.configSiphon <- configs

		// Wait for server creation
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return capturedConfig != nil
		}, time.Second, 10*time.Millisecond)

		// Verify parameters
		mu.Lock()
		assert.NotNil(t, capturedCtx)
		assert.Equal(t, testConfig, capturedConfig)
		assert.NotNil(t, capturedHandler)
		mu.Unlock()

		cancel()
	})
}
