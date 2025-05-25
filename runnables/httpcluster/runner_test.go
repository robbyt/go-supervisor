package httpcluster

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
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

func (m *MockEntriesManager) commit() EntriesManager {
	args := m.Called()
	return args.Get(0).(EntriesManager)
}

func (m *MockEntriesManager) setRuntime(
	id string,
	runner *httpserver.Runner,
	ctx context.Context,
	cancel context.CancelFunc,
	stateSub <-chan string,
) EntriesManager {
	args := m.Called(id, runner, ctx, cancel, stateSub)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(EntriesManager)
}

func (m *MockEntriesManager) clearRuntime(id string) EntriesManager {
	args := m.Called(id)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(EntriesManager)
}

func TestNewRunner(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)
		require.NotNil(t, runner)

		assert.Equal(t, 0, runner.siphonBuffer)
		assert.NotNil(t, runner.logger)
		assert.NotNil(t, runner.parentCtx)
		assert.NotNil(t, runner.configSiphon)
		assert.NotNil(t, runner.currentEntries)
		assert.NotNil(t, runner.fsm)
		assert.Equal(t, finitestate.StatusNew, runner.GetState())
	})

	t.Run("with options", func(t *testing.T) {
		logger := testLogger
		ctx := context.Background()

		runner, err := NewRunner(
			WithLogger(logger),
			WithContext(ctx),
			WithSiphonBuffer(10),
		)
		require.NoError(t, err)

		assert.Equal(t, 10, runner.siphonBuffer)
		assert.Equal(t, ctx, runner.parentCtx)
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
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
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
	t.Run("basic run cycle", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

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

		ctx := context.Background()

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

		ctx := context.Background()

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
	t.Run("config update when running", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

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
			"server1": createTestConfig(t, ":8001"),
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
		<-runErr
	})

	t.Run("config update when not running", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Don't start runner - should ignore config updates
		configs := map[string]*httpserver.Config{
			"server1": createTestConfig(t, ":8001"),
		}

		err = runner.processConfigUpdate(context.Background(), configs)
		assert.NoError(t, err) // Should not error, just ignore

		assert.Equal(t, finitestate.StatusNew, runner.GetState())
	})
}

func TestRunnerStateTransitions(t *testing.T) {
	t.Run("normal state flow", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Track state changes
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		stateChan := runner.GetStateChan(ctx)
		states := []string{}
		statesMu := sync.Mutex{}

		go func() {
			for state := range stateChan {
				statesMu.Lock()
				states = append(states, state)
				statesMu.Unlock()
			}
		}()

		// Start runner
		runCtx, runCancel := context.WithCancel(context.Background())
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
		<-runErr

		// Check state progression
		require.Eventually(t, func() bool {
			statesMu.Lock()
			defer statesMu.Unlock()
			return len(states) >= 4
		}, time.Second, 10*time.Millisecond)

		statesMu.Lock()
		defer statesMu.Unlock()

		// Should see: New -> Booting -> Running -> Stopping -> Stopped
		assert.Contains(t, states, finitestate.StatusNew)
		assert.Contains(t, states, finitestate.StatusBooting)
		assert.Contains(t, states, finitestate.StatusRunning)
		assert.Contains(t, states, finitestate.StatusStopping)
		assert.Contains(t, states, finitestate.StatusStopped)
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
	t.Run("concurrent config updates", func(t *testing.T) {
		runner, err := NewRunner(WithSiphonBuffer(10))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

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
					"server1": createTestConfig(t, ":8001"),
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

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

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
	t.Run("execute with no actions", func(t *testing.T) {
		runner, err := NewRunner()
		require.NoError(t, err)

		// Setup run context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
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

		result := runner.executeActions(context.Background(), mockEntries)
		assert.Equal(t, mockEntries, result)

		mockEntries.AssertExpectations(t)
	})
}

func TestRunnerContextManagement(t *testing.T) {
	t.Run("parent context cancellation", func(t *testing.T) {
		parentCtx, parentCancel := context.WithCancel(context.Background())

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

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

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
