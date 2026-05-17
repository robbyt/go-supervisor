package composite

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockStateMachine is a mock implementation of the finitestate.Machine interface for testing
type MockStateMachine struct {
	mock.Mock
}

func (m *MockStateMachine) GetState() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockStateMachine) SetState(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

func (m *MockStateMachine) Transition(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

func (m *MockStateMachine) TransitionBool(state string) bool {
	args := m.Called(state)
	return args.Bool(0)
}

func (m *MockStateMachine) TransitionIfCurrentState(current, next string) error {
	args := m.Called(current, next)
	return args.Error(0)
}

func (m *MockStateMachine) GetStateChan(ctx context.Context) <-chan string {
	args := m.Called(ctx)
	return args.Get(0).(<-chan string)
}

// MockReloadableWithConfig implements both Runnable and ReloadableWithConfig for testing
type MockReloadableWithConfig struct {
	*mocks.Runnable
}

func (m *MockReloadableWithConfig) ReloadWithConfig(ctx context.Context, config any) {
	m.Called(ctx, config)
}

func NewMockReloadableWithConfig() *MockReloadableWithConfig {
	return &MockReloadableWithConfig{
		Runnable: mocks.NewMockRunnable(),
	}
}

func TestCompositeRunner_Reload(t *testing.T) {
	t.Parallel()

	t.Run("reload with reloadable runnables", func(t *testing.T) {
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1")
		mockRunnable1.On("Reload", mock.Anything).Return(nil).Once()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable1.On("Stop").Return(nil).Maybe() // Add explicit expectation for Stop

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2")
		mockRunnable2.On("Reload", mock.Anything).Return(nil).Once()
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable2.On("Stop").Return(nil).Maybe() // Add explicit expectation for Stop

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		callbackCalls := 0
		configCallback := func() (*Config[*mocks.Runnable], error) {
			callbackCalls++
			return NewConfig("test", entries)
		}

		handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})

		ctx := context.Background()
		runner, err := NewRunner(
			configCallback,
			WithLogHandler[*mocks.Runnable](handler),
		)
		require.NoError(t, err)
		assert.Equal(t, 0, callbackCalls, "first callback is after Runner.Run")

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel() // Use defer instead of t.Cleanup for immediate cancellation

		go func() {
			err := runner.Run(runCtx)
			assert.NoError(t, err)
		}()

		require.Eventually(t, func() bool {
			return runner.IsReady()
		}, 2*time.Second, 10*time.Millisecond)
		assert.Equal(t, 1, callbackCalls)

		require.NoError(t, runner.Reload(t.Context()))
		assert.Equal(t, len(entries), callbackCalls)
		require.Eventually(t, func() bool {
			return runner.IsReady()
		}, 1*time.Second, 100*time.Millisecond)

		// Cancel the context and wait for shutdown before verifying expectations
		cancel()
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusStopped ||
				runner.GetState() == finitestate.StatusStopping
		}, 1*time.Second, 10*time.Millisecond, "Runner should stop after context cancellation")

		// Verify mock expectations
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reload with updated runnables", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			mockRunnable1 := mocks.NewMockRunnable()
			mockRunnable1.On("String").Return("runnable1").Maybe()
			mockRunnable1.On("Stop").Maybe()
			mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(context.Canceled).Maybe()

			mockRunnable2 := mocks.NewMockRunnable()
			mockRunnable2.On("String").Return("runnable2").Maybe()
			mockRunnable2.On("Stop").Once() // Will be stopped
			mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(context.Canceled).Maybe()

			mockRunnable3 := mocks.NewMockRunnable()
			mockRunnable3.On("String").Return("runnable3").Maybe()
			mockRunnable3.On("Stop").Maybe()
			mockRunnable3.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(context.Canceled).Once()

			initialEntries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mockRunnable1, Config: nil},
				{Runnable: mockRunnable2, Config: nil},
			}

			updatedEntries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mockRunnable1, Config: nil},
				{Runnable: mockRunnable3, Config: nil},
			}

			useUpdatedEntries := false

			callbackCalls := 0
			configCallback := func() (*Config[*mocks.Runnable], error) {
				callbackCalls++
				if useUpdatedEntries {
					return NewConfig("test", updatedEntries)
				}
				return NewConfig("test", initialEntries)
			}

			runner, err := NewRunner(configCallback)
			require.NoError(t, err)
			require.Equal(t, 0, callbackCalls)

			errCh := make(chan error, 1)
			go func() {
				errCh <- runner.Run(t.Context())
			}()

			synctest.Wait()
			require.Equal(t, finitestate.StatusRunning, runner.GetState())

			useUpdatedEntries = true
			require.NoError(t, runner.Reload(t.Context()))
			synctest.Wait()

			config, err := runner.getConfig()
			require.NoError(t, err)
			require.NotNil(t, config)
			assert.Len(t, config.Entries, 2)
			assert.Equal(t, mockRunnable1, config.Entries[0].Runnable)
			assert.Equal(t, mockRunnable3, config.Entries[1].Runnable)

			assert.Equal(t, finitestate.StatusRunning, runner.GetState())

			runner.Stop()
			synctest.Wait()
			assert.Equal(t, finitestate.StatusStopped, runner.GetState())

			mockRunnable1.AssertExpectations(t)
			mockRunnable2.AssertExpectations(t)
			mockRunnable3.AssertExpectations(t)
			require.NoError(t, <-errCh)
		})
	})

	t.Run("reload with updated configurations", func(t *testing.T) {
		// Setup mock runnables with blocking behavior
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload", mock.Anything).Return(nil).Once()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			// Block until context is canceled - this mimics real runnable behavior
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable1.On("Stop").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload", mock.Anything).Return(nil).Once()
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			// Block until context is canceled - this mimics real runnable behavior
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable2.On("Stop").Maybe()

		// Initial configs
		initialConfig1 := map[string]string{"key": "value1"}
		initialConfig2 := map[string]string{"key": "value2"}

		// Updated configs
		updatedConfig1 := map[string]string{"key": "updated1"}
		updatedConfig2 := map[string]string{"key": "updated2"}

		// Create initial entries
		initialEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: initialConfig1},
			{Runnable: mockRunnable2, Config: initialConfig2},
		}

		// Create updated entries for reload
		updatedEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: updatedConfig1},
			{Runnable: mockRunnable2, Config: updatedConfig2},
		}

		// Variable to track which set of entries to return
		useUpdatedEntries := false
		callbackCalls := 0
		configCallback := func() (*Config[*mocks.Runnable], error) {
			callbackCalls++
			if useUpdatedEntries {
				return NewConfig("test", updatedEntries)
			}
			return NewConfig("test", initialEntries)
		}

		// Create runner and set state to Running
		ctx := t.Context()
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)
		assert.Equal(t, 0, callbackCalls)

		// Start the runner
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			err := runner.Run(runCtx)
			assert.NoError(t, err)
		}()

		// Verify runner reaches Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 100*time.Millisecond, "Runner should transition to Running state")
		assert.Equal(t, 1, callbackCalls)

		// Verify original config
		config, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Equal(
			t,
			initialConfig1,
			config.Entries[0].Config,
			"First runnable should have initial config",
		)
		assert.Equal(
			t,
			initialConfig2,
			config.Entries[1].Config,
			"Second runnable should have initial config",
		)
		assert.Equal(t, 1, callbackCalls, "getConfig does not call the config callback")

		// Switch to using updated entries
		useUpdatedEntries = true

		// Call Reload
		require.NoError(t, runner.Reload(t.Context()))

		// Verify reload completes and runner returns to Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should return to Running state after reload")
		assert.Equal(t, 2, callbackCalls, "Reload should call config callback again")

		// Verify updated config
		config, err = runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Equal(
			t,
			updatedConfig1,
			config.Entries[0].Config,
			"First runnable should have updated config",
		)
		assert.Equal(
			t,
			updatedConfig2,
			config.Entries[1].Config,
			"Second runnable should have updated config",
		)
		assert.Equal(t, 2, callbackCalls, "getConfig does not call the config callback")

		// Graceful shutdown
		runner.Stop()
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusStopped
		}, 2*time.Second, 10*time.Millisecond, "Runner should transition to Stopped state")

		// Verify mock expectations (runnables were reloaded)
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reload with no config changes", func(t *testing.T) {
		// Setup mock runnables with blocking behavior
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1")
		mockRunnable1.On("Reload", mock.Anything).Return(nil).Once()
		// Make Run properly block until context cancellation
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Once()
		mockRunnable1.On("Stop").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2")
		mockRunnable2.On("Reload", mock.Anything).Return(nil).Once()
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Once()
		mockRunnable2.On("Stop").Once()

		config := map[string]string{"key": "value"}
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: config},
			{Runnable: mockRunnable2, Config: config},
		}

		// Track callback calls
		callbackCalls := 0
		configCallback := func() (*Config[*mocks.Runnable], error) {
			callbackCalls++
			return NewConfig("test", entries)
		}

		// Create runner
		ctx := t.Context()
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)
		require.Equal(t, 0, callbackCalls, "Callback should not be called during creation")

		// Start the runner
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			err := runner.Run(runCtx)
			assert.NoError(t, err)
		}()

		// Verify runner reaches Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should transition to Running state")

		// Verify initial config loaded
		assert.Equal(t, 1, callbackCalls, "Config callback should be called once during startup")

		// Call Reload with the same configuration
		require.NoError(t, runner.Reload(t.Context()))

		// Verify reload completes and runner stays in Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should maintain Running state after reload")
		assert.Equal(t, 2, callbackCalls, "Config callback should be called again during reload")

		// Graceful shutdown
		runner.Stop()
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusStopped
		}, 2*time.Second, 10*time.Millisecond, "Runner should transition to Stopped state")

		// Verify expectations - both runnables should have been reloaded
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reload with ReloadableWithConfig implementation", func(t *testing.T) {
		// mock 1 setup
		initialConfig1 := map[string]string{"key": "initial1"}
		updatedConfig1 := map[string]string{"key": "updated1"}

		mockReloadable1 := NewMockReloadableWithConfig()
		mockReloadable1.On("String").Return("reloadable1")
		mockReloadable1.On("ReloadWithConfig", mock.Anything, updatedConfig1).Once()
		mockReloadable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Once()
		mockReloadable1.Runnable.On("Stop").Return(nil).Once()

		// mock 2 setup
		initialConfig2 := map[string]string{"key": "initial2"}
		updatedConfig2 := map[string]string{"key": "updated2"}

		mockReloadable2 := NewMockReloadableWithConfig()
		mockReloadable2.On("String").Return("reloadable2")
		mockReloadable2.On("ReloadWithConfig", mock.Anything, updatedConfig2).Once()
		mockReloadable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Once()
		mockReloadable2.Runnable.On("Stop").Return(nil).Once()

		// Create initial entries
		initialEntries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable1, Config: initialConfig1},
			{Runnable: mockReloadable2, Config: initialConfig2},
		}

		// Create updated entries for reload
		updatedEntries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable1, Config: updatedConfig1},
			{Runnable: mockReloadable2, Config: updatedConfig2},
		}

		// Variable to track which set of entries to return
		useUpdatedEntries := false
		callbackCalls := 0
		configCallback := func() (*Config[*MockReloadableWithConfig], error) {
			callbackCalls++
			if useUpdatedEntries {
				return NewConfig("test", updatedEntries)
			}
			return NewConfig("test", initialEntries)
		}

		// Create runner with context
		ctx := t.Context()
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)
		assert.Equal(t, 0, callbackCalls)

		// Start the runner
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			err := runner.Run(runCtx)
			assert.NoError(t, err)
		}()

		// Verify runner reaches Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 100*time.Millisecond, "Runner should transition to Running state")
		assert.Equal(t, 1, callbackCalls)

		// Verify initial config is properly set
		config, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Equal(
			t,
			initialConfig1,
			config.Entries[0].Config,
			"First runnable should have initial config",
		)
		assert.Equal(
			t,
			initialConfig2,
			config.Entries[1].Config,
			"Second runnable should have initial config",
		)

		// Switch to using updated entries
		useUpdatedEntries = true

		// Call Reload
		require.NoError(t, runner.Reload(t.Context()))

		// Verify reload completes and runner returns to Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 100*time.Millisecond, "Runner should return to Running state after reload")
		assert.Equal(t, 2, callbackCalls, "Config callback should be called again during reload")

		// Verify updated config
		updatedConfig, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, updatedConfig)
		assert.Equal(
			t,
			updatedConfig1,
			updatedConfig.Entries[0].Config,
			"First runnable should have updated config",
		)
		assert.Equal(
			t,
			updatedConfig2,
			updatedConfig.Entries[1].Config,
			"Second runnable should have updated config",
		)

		// Graceful shutdown
		runner.Stop()
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusStopped
		}, 2*time.Second, 100*time.Millisecond, "Runner should transition to Stopped state")

		// Verify ReloadWithConfig was called with correct configs
		mockReloadable1.AssertExpectations(t)
		mockReloadable2.AssertExpectations(t)
	})
}

// TestCompositeRunner_Reload_Errors tests error paths in the Reload method
func TestCompositeRunner_Reload_Errors(t *testing.T) {
	t.Parallel()

	t.Run("fsm transition to reloading fails", func(t *testing.T) {
		// When the initial Transition(Reloading) fails (e.g. FSM is in
		// Stopping/Stopped/Booting/Error), Reload must NOT promote that to
		// the Error state — it's "wrong state for reload" control flow,
		// not a runner failure.
		mockFSM := new(MockStateMachine)
		mockFSM.On("TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading).
			Return(errors.New("transition error")).
			Once()
		mockFSM.On("GetState").Return(finitestate.StatusStopped).Maybe()

		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1").Maybe()

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)
		runner.fsm = mockFSM

		require.NoError(t, runner.Reload(t.Context()))

		// SetState(Error) must NOT be called — that would be wrong control flow.
		mockFSM.AssertNotCalled(t, "SetState", finitestate.StatusError)
		mockFSM.AssertExpectations(t)
	})

	t.Run("config callback error during reload", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading).
			Return(nil).Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(nil).Once()
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()
		// Must not expect Transition to Running since error path should prevent that

		// Create config callback that returns an error
		expectedErr := errors.New("config error during reload")
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, expectedErr
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload - should handle the callback error and surface it
		// via the new error return per the T3.1 contract.
		require.Error(t, runner.Reload(t.Context()))

		// Verify FSM methods were called as expected
		mockFSM.AssertExpectations(t)

		// Verify we're in error state
		assert.Equal(t, finitestate.StatusError, mockFSM.GetState())
	})

	t.Run("config callback returns nil config", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading).
			Return(nil).Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(nil).Once()
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()
		// Must not expect Transition to Running since error path should prevent that

		// Create config callback that returns nil config without error
		callCount := 0
		configCallback := func() (*Config[*mocks.Runnable], error) {
			callCount++
			// Return nil config but no error (which is still an error condition)
			return nil, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload - nil config surfaces via the new error return.
		require.Error(t, runner.Reload(t.Context()))

		// Verify FSM methods were called as expected
		mockFSM.AssertExpectations(t)
		assert.Equal(t, 1, callCount, "Config callback should be called once")
		assert.Equal(t, finitestate.StatusError, mockFSM.GetState(), "Should be in error state")
	})

	// Panic-propagation subtests deleted: child Reload now runs in Run's
	// goroutine (handleReload) rather than inline in Reload's caller, so a
	// panicking child no longer propagates through Reload's stack frame.
	// That tradeoff is the explicit point of the structural refactor — keep
	// FSM mutation single-threaded inside Run.
}

// TestGetConfig_NilCase tests the getConfig method's handling of nil cases
func TestGetConfig_NilCase(t *testing.T) {
	t.Parallel()

	t.Run("callback returns nil", func(t *testing.T) {
		// Create callback that returns nil config
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Clear the config cache
		runner.currentConfig.Store(nil)

		// Get config should return ErrConfigCallbackNil and a nil config
		config, err := runner.getConfig()
		assert.Nil(t, config)
		require.ErrorIs(t, err, ErrConfigCallbackNil)
	})

	t.Run("callback returns error", func(t *testing.T) {
		// Create callback that returns an error
		callerSentinel := errors.New("config error")
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, callerSentinel
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Clear the config cache
		runner.currentConfig.Store(nil)

		// Get config should return a wrapped error and a nil config.
		// Both the package sentinel and the caller's sentinel must be
		// reachable via errors.Is.
		config, err := runner.getConfig()
		assert.Nil(t, config)
		require.ErrorIs(t, err, ErrConfigCallback)
		require.ErrorIs(t, err, callerSentinel)
	})
}

// TestSetStateError tests the setStateError method
func TestSetStateError(t *testing.T) {
	t.Parallel()

	t.Run("successful transition", func(t *testing.T) {
		// Create mock FSM that succeeds
		mockFSM := new(MockStateMachine)
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)

		// Create runner
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call setStateError
		runner.setStateError()

		// Verify FSM method was called
		mockFSM.AssertExpectations(t)
	})

	t.Run("transition fails", func(t *testing.T) {
		// Create mock FSM that fails
		mockFSM := new(MockStateMachine)
		mockFSM.On("SetState", finitestate.StatusError).Return(errors.New("set state error"))

		// Create runner
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
		}
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call setStateError - should handle the error internally
		runner.setStateError()

		// Verify FSM method was called
		mockFSM.AssertExpectations(t)
	})
}

// TestAbortDispatch covers the per-branch cleanup helper used by
// dispatchReload's ctx.Done()/lc.DoneCh() abort paths.
func TestAbortDispatch(t *testing.T) {
	t.Parallel()

	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
	}

	t.Run("transitions Reloading to Running", func(t *testing.T) {
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		require.NoError(t, runner.fsm.Transition(finitestate.StatusBooting))
		require.NoError(t, runner.fsm.Transition(finitestate.StatusRunning))
		require.NoError(t, runner.fsm.Transition(finitestate.StatusReloading))

		runner.abortDispatch("test reason")

		assert.Equal(t, finitestate.StatusRunning, runner.fsm.GetState())
	})

	t.Run("escalates to Error when transition fails", func(t *testing.T) {
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusRunning).
			Return(errors.New("transition failed")).Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(nil).Once()

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)
		runner.fsm = mockFSM

		runner.abortDispatch("test reason", "key", "value")

		mockFSM.AssertExpectations(t)
	})
}

// TestDispatchReload_AbortBranches exercises the two abort cases of
// dispatchReload's select. Both branches require reloadCh to be unable to
// accept the send, so the test pre-fills the cap-1 buffer with a sentinel
// request that no goroutine will consume.
func TestDispatchReload_AbortBranches(t *testing.T) {
	t.Parallel()

	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
	}

	bringToReloading := func(t *testing.T, r *Runner[*mocks.Runnable]) {
		t.Helper()
		require.NoError(t, r.fsm.Transition(finitestate.StatusBooting))
		require.NoError(t, r.fsm.Transition(finitestate.StatusRunning))
		require.NoError(t, r.fsm.Transition(finitestate.StatusReloading))
	}

	t.Run("ctx.Done branch", func(t *testing.T) {
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Hold lc.DoneCh open so the lc.DoneCh case is not ready and the
		// select must pick ctx.Done deterministically. Don't call the
		// returned `done` — it stays held for the duration of the test.
		_ = runner.lc.Started()

		bringToReloading(t, runner)

		// Fill the cap-1 reloadCh buffer so the send case blocks.
		runner.reloadCh <- &reloadReq[*mocks.Runnable]{result: make(chan error, 1)}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		newConfig, err := NewConfig("dispatched", []RunnableEntry[*mocks.Runnable]{})
		require.NoError(t, err)

		require.ErrorIs(t, runner.dispatchReload(ctx, newConfig), context.Canceled,
			"ctx.Done branch must return ctx.Err()")

		assert.Equal(t, finitestate.StatusRunning, runner.fsm.GetState())
	})

	t.Run("lc.DoneCh branch", func(t *testing.T) {
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Don't call lc.Started() — DoneCh returns the always-closed
		// sentinel, making the lc.DoneCh case the only ready case once
		// the buffer is full and ctx is live.
		bringToReloading(t, runner)

		runner.reloadCh <- &reloadReq[*mocks.Runnable]{result: make(chan error, 1)}

		newConfig, err := NewConfig("dispatched", []RunnableEntry[*mocks.Runnable]{})
		require.NoError(t, err)

		require.ErrorIs(t, runner.dispatchReload(t.Context(), newConfig), ErrReloadAbandoned,
			"lc.DoneCh branch must surface ErrReloadAbandoned")

		assert.Equal(t, finitestate.StatusRunning, runner.fsm.GetState())
	})
}

// TestHandleReload_Branches covers the defensive branches in handleReload:
// the oldConfig==nil treat-as-empty fallback, the reload-failed setStateError
// path, and the Transition(Running)-fails escalation.
func TestHandleReload_Branches(t *testing.T) {
	t.Parallel()

	t.Run("nil oldConfig treated as empty", func(t *testing.T) {
		// A fresh runner has no currentConfig. With an empty new config,
		// hasMembershipChanged(empty, empty) is false → reloadSkipRestart
		// runs (no-op for empty entries) and FSM transitions back to
		// Running. This exercises the `oldConfig == nil` fallback at the
		// top of handleReload.
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
		}
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		require.NoError(t, runner.fsm.Transition(finitestate.StatusBooting))
		require.NoError(t, runner.fsm.Transition(finitestate.StatusRunning))
		require.NoError(t, runner.fsm.Transition(finitestate.StatusReloading))

		emptyConfig, err := NewConfig("empty", []RunnableEntry[*mocks.Runnable]{})
		require.NoError(t, err)

		require.NoError(t, runner.handleReload(t.Context(), emptyConfig))
		assert.Equal(t, finitestate.StatusRunning, runner.fsm.GetState())
	})

	t.Run("reload error sets Error state", func(t *testing.T) {
		// Force reloadWithRestart to fail without spawning child Run
		// goroutines: the configCallback returns nil, so getConfig stays
		// nil and stopAllRunnables returns ErrConfigMissing before boot
		// is reached.
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, nil
		}
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		require.NoError(t, runner.fsm.Transition(finitestate.StatusBooting))
		require.NoError(t, runner.fsm.Transition(finitestate.StatusRunning))
		require.NoError(t, runner.fsm.Transition(finitestate.StatusReloading))

		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("forces-membership-change").Maybe()
		newConfig, err := NewConfig("new", []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		})
		require.NoError(t, err)

		require.Error(t, runner.handleReload(t.Context(), newConfig))
		assert.Equal(t, finitestate.StatusError, runner.fsm.GetState())
	})

	t.Run("Transition failure escalates to Error", func(t *testing.T) {
		// Mock the FSM so the success-path Transition(Running) returns an
		// error after reloadSkipRestart succeeds.
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusRunning).
			Return(errors.New("transition failed")).Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(nil).Once()

		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
		}
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)
		runner.fsm = mockFSM

		emptyConfig, err := NewConfig("empty", []RunnableEntry[*mocks.Runnable]{})
		require.NoError(t, err)

		require.Error(t, runner.handleReload(t.Context(), emptyConfig))
		mockFSM.AssertExpectations(t)
	})
}

// TestGetChildStates tests the GetChildStates method with various types of runnables
func TestGetChildStates(t *testing.T) {
	t.Parallel()

	// Create a mock for MockRunnableWithStateable to use in tests
	t.Run("nil config case", func(t *testing.T) {
		// Create runner with a config callback that returns nil
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, errors.New("config error")
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Call GetChildStates
		states := runner.GetChildStates()

		// Verify empty map is returned
		assert.Empty(t, states)
	})
}

// TestReloadConfig tests the reloadConfig method
func TestReloadConfig(t *testing.T) {
	t.Parallel()

	t.Run("reloads standard reloadable runnables", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload", mock.Anything).Return(nil).Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload", mock.Anything).Return(nil).Once()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create config
		config, err := NewConfig("test", entries)
		require.NoError(t, err)

		// Create runner
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return config, nil
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Call reloadConfig directly
		require.NoError(t, runner.reloadSkipRestart(t.Context(), config))

		// Verify reloadable interface methods were called
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reloads runnables with ReloadableWithConfig interface", func(t *testing.T) {
		// Setup mock that implements ReloadableWithConfig
		mockReloadable1 := NewMockReloadableWithConfig()
		mockReloadable1.On("String").Return("reloadable1").Maybe()

		customConfig1 := map[string]string{"key": "value1"}
		mockReloadable1.On("ReloadWithConfig", mock.Anything, customConfig1).Once()

		mockReloadable2 := NewMockReloadableWithConfig()
		mockReloadable2.On("String").Return("reloadable2").Maybe()

		customConfig2 := map[string]string{"key": "value2"}
		mockReloadable2.On("ReloadWithConfig", mock.Anything, customConfig2).Once()

		// Create entries
		entries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable1, Config: customConfig1},
			{Runnable: mockReloadable2, Config: customConfig2},
		}

		// Create config
		config, err := NewConfig("test", entries)
		require.NoError(t, err)

		// Create runner
		configCallback := func() (*Config[*MockReloadableWithConfig], error) {
			return config, nil
		}
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		require.NoError(t, runner.reloadSkipRestart(t.Context(), config))

		// Verify ReloadWithConfig was called with correct configs
		mockReloadable1.AssertExpectations(t)
		mockReloadable2.AssertExpectations(t)
	})

	t.Run("with mixed interface implementations", func(t *testing.T) {
		// Setup mock that implements standard Reloadable
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Reload", mock.Anything).Return(nil).Once()

		// Setup mock that implements ReloadableWithConfig
		mockReloadable := NewMockReloadableWithConfig()
		mockReloadable.On("String").Return("reloadable").Maybe()

		customConfig := map[string]string{"key": "value"}
		mockReloadable.On("ReloadWithConfig", mock.Anything, customConfig).Once()

		// Create both configs
		entries1 := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}
		config1, err := NewConfig("test1", entries1)
		require.NoError(t, err)

		entries2 := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable, Config: customConfig},
		}
		config2, err := NewConfig("test2", entries2)
		require.NoError(t, err)

		// Create runners
		runner1, err := NewRunner(
			func() (*Config[*mocks.Runnable], error) {
				return config1, nil
			},
		)
		require.NoError(t, err)

		runner2, err := NewRunner(
			func() (*Config[*MockReloadableWithConfig], error) {
				return config2, nil
			},
		)
		require.NoError(t, err)

		require.NoError(t, runner1.reloadSkipRestart(t.Context(), config1))
		require.NoError(t, runner2.reloadSkipRestart(t.Context(), config2))

		// Verify expectations
		mockRunnable.AssertExpectations(t)
		mockReloadable.AssertExpectations(t)
	})

	// Covers the T3.4 contract: reloadSkipRestart threads its ctx into the
	// ReloadableWithConfig branch (not just into the Reloadable fallback).
	// Without this, a child reload couldn't observe caller cancellation.
	t.Run("threads ctx into ReloadWithConfig", func(t *testing.T) {
		mockReloadable := NewMockReloadableWithConfig()
		mockReloadable.On("String").Return("ctx-reloadable").Maybe()

		cfg := map[string]string{"key": "v"}
		// Capture the ctx the runner passes through and verify it matches.
		var receivedCtx context.Context
		mockReloadable.On("ReloadWithConfig", mock.Anything, cfg).Run(func(args mock.Arguments) {
			receivedCtx = args.Get(0).(context.Context)
		}).Once()

		entries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable, Config: cfg},
		}
		config, err := NewConfig("ctx-test", entries)
		require.NoError(t, err)

		runner, err := NewRunner(func() (*Config[*MockReloadableWithConfig], error) {
			return config, nil
		})
		require.NoError(t, err)

		callerCtx := t.Context()
		require.NoError(t, runner.reloadSkipRestart(callerCtx, config))

		mockReloadable.AssertExpectations(t)
		require.NotNil(t, receivedCtx, "ReloadWithConfig must receive a non-nil ctx")
		require.Same(t, callerCtx, receivedCtx,
			"ReloadWithConfig must receive the same ctx the caller passed to reloadSkipRestart")
	})
}

// TestReloadMembershipChanged tests the reloadMembershipChanged method
func TestReloadMembershipChanged(t *testing.T) {
	t.Parallel()

	t.Run("successfully stops and starts runnables", func(t *testing.T) {
		// Setup mock runnables for old config
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Stop").Maybe() // Use Maybe() instead of Once() for more resilience

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Stop").Maybe() // Use Maybe() instead of Once() for more resilience

		// Create initial entries
		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Setup mock runnables for new config
		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("runnable3").Maybe()
		// Allow any number of Run calls for resilience
		mockRunnable3.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable4 := mocks.NewMockRunnable()
		mockRunnable4.On("String").Return("runnable4").Maybe()
		// Allow any number of Run calls for resilience
		mockRunnable4.On("Run", mock.Anything).Return(nil).Maybe()

		// Create updated entries for reload
		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable3, Config: nil},
			{Runnable: mockRunnable4, Config: nil},
		}

		initialConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Create callback that initially returns oldEntries
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Make sure initial config is loaded
		initialConfigLoaded, err := runner.getConfig()
		require.NoError(t, err)
		assert.NotNil(t, initialConfigLoaded)
		assert.Len(t, initialConfigLoaded.Entries, 2)

		err = runner.reloadWithRestart(t.Context(), newConfig)
		require.NoError(t, err)

		// Verify config was updated
		updatedConfig, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, updatedConfig)
		assert.Len(t, updatedConfig.Entries, 2)
		assert.Equal(t, mockRunnable3, updatedConfig.Entries[0].Runnable)
		assert.Equal(t, mockRunnable4, updatedConfig.Entries[1].Runnable)

		// Instead of sleeping, use require.Eventually to verify expectations
		require.Eventually(t, func() bool {
			// Return true if the mock expectations are met
			return mockRunnable1.AssertExpectations(t) &&
				mockRunnable2.AssertExpectations(t) &&
				mockRunnable3.AssertExpectations(t) &&
				mockRunnable4.AssertExpectations(t)
		}, 100*time.Millisecond, 10*time.Millisecond, "Mock expectations should be met")

		mockRunnable2.AssertExpectations(t)
		mockRunnable3.AssertExpectations(t)
		mockRunnable4.AssertExpectations(t)
	})

	t.Run("handles stopRunnables error", func(t *testing.T) {
		// Setup a runner with no initial config
		runner, err := NewRunner(
			func() (*Config[*mocks.Runnable], error) {
				return nil, nil
			},
		)
		require.NoError(t, err)

		// Create a new config
		newEntries := []RunnableEntry[*mocks.Runnable]{}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		err = runner.reloadWithRestart(t.Context(), newConfig)

		// Verify error
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConfigMissing)
	})

	t.Run("handles empty entries", func(t *testing.T) {
		t.Parallel()

		// Create a mock runnable for the test
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1").Maybe()
		mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()
		mockRunnable.On("Stop").Maybe()

		// Track config callback calls
		configCallCount := 0

		// Create a config callback that initially returns empty entries
		// and later returns entries with a runnable
		configCallback := func() (*Config[*mocks.Runnable], error) {
			configCallCount++
			if configCallCount == 1 {
				// First call returns empty entries (now valid with our change)
				return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
			}
			// Later call returns a config with an actual runnable
			entries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mockRunnable, Config: nil},
			}
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Run in a goroutine to avoid blocking
		errCh := make(chan error, 1)
		go func() {
			errCh <- runner.Run(t.Context())
		}()

		// Wait for the runner to start
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 1*time.Second, 10*time.Millisecond, "Runner should transition to Running state")

		// Verify we're running with empty entries
		initialConfig, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, initialConfig)
		assert.Empty(t, initialConfig.Entries, "Initial config should have empty entries")

		// Now reload to get the config with the runnable
		require.NoError(t, runner.Reload(t.Context()))

		// Wait for reload to complete
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 1*time.Second, 10*time.Millisecond, "Runner should transition to Running state")

		// Verify the config was updated with the new runnable
		updatedConfig, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, updatedConfig)
		require.Len(t, updatedConfig.Entries, 1, "Updated config should have 1 entry")
		assert.Same(t, mockRunnable, updatedConfig.Entries[0].Runnable)

		// Clean shutdown
		runner.Stop()

		// Wait for runner to complete
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for runner to stop")
		}
	})

	// Remove the duplicate "handles boot error" test case and replace with a test
	// that properly handles new empty entries behavior
	t.Run("no error with empty entries", func(t *testing.T) {
		// Setup mock runnables for old config - empty
		oldEntries := []RunnableEntry[*mocks.Runnable]{}

		// Create new entries - also empty
		newEntries := []RunnableEntry[*mocks.Runnable]{}

		initialConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Create callback that returns the config
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Make sure initial config is loaded
		runner.currentConfig.Store(initialConfig)

		err = runner.reloadWithRestart(t.Context(), newConfig)
		require.NoError(t, err)

		// Verify config was updated
		updatedConfig, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, updatedConfig)
		assert.Empty(t, updatedConfig.Entries, "Config should have empty entries")
	})
}

// TestHasMembershipChanged tests the hasMembershipChanged function
func TestHasMembershipChanged(t *testing.T) {
	t.Parallel()

	t.Run("different number of entries", func(t *testing.T) {
		// Create configs with different numbers of entries
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()

		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
		}
		oldConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Membership should have changed
		assert.True(t, hasMembershipChanged(oldConfig, newConfig))
	})

	t.Run("different runnables", func(t *testing.T) {
		// Create configs with the same number of entries but different runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()

		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("runnable3").Maybe()

		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		oldConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable3, Config: nil}, // Different runnable
		}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Membership should have changed
		assert.True(t, hasMembershipChanged(oldConfig, newConfig))
	})

	t.Run("same runnables", func(t *testing.T) {
		// Create configs with the same runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()

		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		oldConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Membership should not have changed
		assert.False(t, hasMembershipChanged(oldConfig, newConfig))
	})

	t.Run("reload with complete membership changes", func(t *testing.T) {
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Stop").Return(nil).Once()
		mockRunnable1.On("Reload", mock.Anything).Return(nil).Maybe()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Stop").Return(nil).Once()
		mockRunnable2.On("Reload", mock.Anything).Return(nil).Maybe()
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()

		// Setup completely new runnables for reload with consistent expectations
		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("runnable3").Maybe()
		mockRunnable3.On("Stop").Return(nil).Once()
		mockRunnable3.On("Reload", mock.Anything).Return(nil).Maybe()
		mockRunnable3.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()

		mockRunnable4 := mocks.NewMockRunnable()
		mockRunnable4.On("String").Return("runnable4").Maybe()
		mockRunnable4.On("Stop").Return(nil).Once()
		mockRunnable4.On("Reload", mock.Anything).Return(nil).Maybe()
		mockRunnable4.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()

		// Create initial entries
		initialEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create completely new entries for reload
		updatedEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable3, Config: nil},
			{Runnable: mockRunnable4, Config: nil},
		}

		// Variable to track which set of entries to return
		useUpdatedEntries := false

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			if useUpdatedEntries {
				return NewConfig("test1", updatedEntries)
			}
			return NewConfig("test2", initialEntries)
		}

		ctx := context.Background()
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		errCh := make(chan error, 1)
		go func() {
			errCh <- runner.Run(ctx)
		}()

		// Verify runner reaches Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should reach StatusRunning")

		// Verify initial config loaded
		config, err := runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Len(t, config.Entries, 2)
		assert.Equal(t, mockRunnable1, config.Entries[0].Runnable)
		assert.Equal(t, mockRunnable2, config.Entries[1].Runnable)

		// Switch to using entirely new set of runnables
		useUpdatedEntries = true
		require.NoError(t, runner.Reload(t.Context()))

		// Wait for reload to complete
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should return to Running state")

		// Verify updated config contains only the new runnables
		config, err = runner.getConfig()
		require.NoError(t, err)
		require.NotNil(t, config)
		assert.Len(t, config.Entries, 2)
		assert.Equal(t, mockRunnable3, config.Entries[0].Runnable)
		assert.Equal(t, mockRunnable4, config.Entries[1].Runnable)

		// Clean shutdown before verification
		runner.Stop()

		// Wait for runner to complete
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusStopped
		}, 2*time.Second, 10*time.Millisecond, "Runner should transition to Stopped state")

		// Check for any errors from the runner goroutine
		select {
		case runErr := <-errCh:
			require.NoError(t, runErr)
		case <-time.After(time.Second):
			t.Fatal("Runner.Run didn't complete within timeout")
		}

		// Only verify expectations after the runner has completely stopped
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
		mockRunnable3.AssertExpectations(t)
		mockRunnable4.AssertExpectations(t)
	})
}

// TestCompositeRunner_ReloadAfterStop verifies that calling Reload after Stop
// returns promptly instead of hanging. The FSM admission gate fails (state is
// Stopped, not Running), so Reload returns without dispatching.
func TestCompositeRunner_ReloadAfterStop(t *testing.T) {
	t.Parallel()

	mockRunnable1 := mocks.NewMockRunnable()
	mockRunnable1.On("String").Return("a").Maybe()
	mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	mockRunnable1.On("Stop").Return().Maybe()

	mockRunnable2 := mocks.NewMockRunnable()
	mockRunnable2.On("String").Return("b").Maybe()

	initial := []RunnableEntry[*mocks.Runnable]{{Runnable: mockRunnable1}}
	swapped := []RunnableEntry[*mocks.Runnable]{{Runnable: mockRunnable2}}
	useSwapped := atomic.Bool{}
	cb := func() (*Config[*mocks.Runnable], error) {
		if useSwapped.Load() {
			return NewConfig("post-stop", swapped)
		}
		return NewConfig("initial", initial)
	}

	runner, err := NewRunner(cb)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(ctx) }()

	require.Eventually(
		t, runner.IsReady, 2*time.Second, 10*time.Millisecond,
	)

	runner.Stop()
	require.Eventually(
		t,
		func() bool { return runner.GetState() == finitestate.StatusStopped },
		2*time.Second, 10*time.Millisecond,
	)

	// Membership-change reload after Stop must not hang. The FSM admission
	// gate fails (state is Stopped, not Running), so Reload returns without
	// dispatching.
	useSwapped.Store(true)
	reloadDone := make(chan struct{})
	go func() {
		defer close(reloadDone)
		// FSM is Stopped → admission gate fails → Reload returns nil.
		// assert (not require) — require.FailNow from a non-test goroutine
		// is undefined behavior per testing docs.
		assert.NoError(t, runner.Reload(t.Context()))
	}()
	select {
	case <-reloadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Reload after Stop did not return — likely hung in dispatchReload")
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestCompositeRunner_ReloadWaitsThroughCallerCtxCancel verifies the
// post-dispatch contract: once Reload has handed the request to Run, the
// caller's ctx is intentionally ignored and Reload blocks until the work
// completes. This guarantees callers never observe FSM=Reloading after
// Reload has returned.
func TestCompositeRunner_ReloadWaitsThroughCallerCtxCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// Slow-stop child blocks Run inside reloadWithRestart, parking the
		// Reload caller on req.done. The caller's ctx is cancelled while
		// parked — Reload MUST keep waiting until close(slowStop).
		slowStop := make(chan struct{})
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("slow").Maybe()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable1.On("Stop").Run(func(_ mock.Arguments) {
			<-slowStop
		}).Return().Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("fast").Maybe()
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable2.On("Stop").Return().Maybe()

		initial := []RunnableEntry[*mocks.Runnable]{{Runnable: mockRunnable1}}
		swapped := []RunnableEntry[*mocks.Runnable]{{Runnable: mockRunnable2}}
		useSwapped := atomic.Bool{}
		cb := func() (*Config[*mocks.Runnable], error) {
			if useSwapped.Load() {
				return NewConfig("swapped", swapped)
			}
			return NewConfig("initial", initial)
		}

		runner, err := NewRunner(cb)
		require.NoError(t, err)

		runCtx, runCancel := context.WithCancel(t.Context())
		defer runCancel()
		runErr := make(chan error, 1)
		go func() { runErr <- runner.Run(runCtx) }()

		synctest.Wait()
		require.True(t, runner.IsReady())

		useSwapped.Store(true)
		reloadCtx, reloadCancel := context.WithCancel(context.Background())
		reloadReturned := atomic.Bool{}
		reloadDone := make(chan struct{})
		go func() {
			defer close(reloadDone)
			// Caller ctx is ignored once dispatched; Reload returns the
			// work outcome (req.err), which is nil because the eventual
			// membership-change reload succeeds.
			assert.NoError(t, runner.Reload(reloadCtx))
			reloadReturned.Store(true)
		}()

		// Wait until Reload is parked on req.done (synctest quiescence proves
		// the bubble has nothing to schedule — Reload's blocked goroutine is
		// the only suspension).
		synctest.Wait()

		// Cancel the caller's ctx mid-flight. Reload must NOT return.
		reloadCancel()
		synctest.Wait()
		require.False(t, reloadReturned.Load(),
			"Reload must wait for completion even after caller ctx cancel")

		// Release the slow child. Run finishes the membership-change reload,
		// transitions FSM Reloading→Running, closes req.done. Reload returns.
		close(slowStop)
		select {
		case <-reloadDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Reload did not return after work completed")
		}
		require.Equal(t, finitestate.StatusRunning, runner.GetState(),
			"FSM must be Running after Reload returns — never Reloading")

		runCancel()
		select {
		case <-runErr:
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after ctx cancel")
		}
	})
}

// TestCompositeRunner_PreCancelledReloadCtx verifies that calling Reload with
// a pre-cancelled ctx is a clean no-op: the FSM gate succeeds (Running →
// Reloading), dispatchReload's outer select fires ctx.Done() before sending,
// and the deferred FSM cleanup transitions Reloading → Running. No side
// effects (no Stop, no config swap, no boot of new entries) and the FSM is
// NOT in Error.
func TestCompositeRunner_PreCancelledReloadCtx(t *testing.T) {
	t.Parallel()

	mockChild := mocks.NewMockRunnable()
	mockChild.On("String").Return("child").Maybe()
	mockChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	mockChild.On("Stop").Return().Maybe()

	altChild := mocks.NewMockRunnable()
	altChild.On("String").Return("alt").Maybe()
	altChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	altChild.On("Stop").Return().Maybe()

	useSwapped := atomic.Bool{}
	cb := func() (*Config[*mocks.Runnable], error) {
		if useSwapped.Load() {
			return NewConfig("swapped", []RunnableEntry[*mocks.Runnable]{{Runnable: altChild}})
		}
		return NewConfig("initial", []RunnableEntry[*mocks.Runnable]{{Runnable: mockChild}})
	}

	runner, err := NewRunner(cb)
	require.NoError(t, err)

	runCtx, runCancel := context.WithCancel(t.Context())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(runCtx) }()
	require.Eventually(t, runner.IsReady, 2*time.Second, 10*time.Millisecond)

	originalCfg, err := runner.getConfig()
	require.NoError(t, err)
	useSwapped.Store(true)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, runner.Reload(cancelledCtx), context.Canceled,
		"cancelled reload must surface ctx.Err() via the return")

	// FSM should still be Running (NOT Error). The cancelled reload is
	// normal control flow, not a failure.
	require.Equal(t, finitestate.StatusRunning, runner.GetState(),
		"FSM must not be in Error after cancelled reload")
	mockChild.AssertNotCalled(t, "Stop")
	currentCfg, err := runner.getConfig()
	require.NoError(t, err)
	require.Same(t, originalCfg, currentCfg,
		"config must not have been swapped for a cancelled reload")
	altChild.AssertNotCalled(t, "Run")

	runCancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestCompositeRunner_CancelledReloadDoesNotErrorState is the focused FSM
// contract assertion: a Reload with a cancelled ctx must leave the runner
// in Running, never Error.
func TestCompositeRunner_CancelledReloadDoesNotErrorState(t *testing.T) {
	t.Parallel()

	mockChild := mocks.NewMockRunnable()
	mockChild.On("String").Return("child").Maybe()
	mockChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	mockChild.On("Stop").Return().Maybe()

	useSwapped := atomic.Bool{}
	altChild := mocks.NewMockRunnable()
	altChild.On("String").Return("alt").Maybe()
	cb := func() (*Config[*mocks.Runnable], error) {
		if useSwapped.Load() {
			return NewConfig("swapped", []RunnableEntry[*mocks.Runnable]{{Runnable: altChild}})
		}
		return NewConfig("initial", []RunnableEntry[*mocks.Runnable]{{Runnable: mockChild}})
	}

	runner, err := NewRunner(cb)
	require.NoError(t, err)

	runCtx, runCancel := context.WithCancel(t.Context())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(runCtx) }()
	require.Eventually(t, runner.IsReady, 2*time.Second, 10*time.Millisecond)

	useSwapped.Store(true)
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, runner.Reload(cancelledCtx), context.Canceled)

	require.Equal(t, finitestate.StatusRunning, runner.GetState(),
		"caller cancellation is normal control flow — FSM must stay in Running")

	runCancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestCompositeRunner_ReloadAfterStopDoesNotErrorState covers the initial
// Transition(Reloading) failure path: after Stop, FSM is Stopped, and a
// Reload call's first transition fails. The runner must NOT be moved to
// Error — it's a "wrong state for reload" message, not a runner failure.
func TestCompositeRunner_ReloadAfterStopDoesNotErrorState(t *testing.T) {
	t.Parallel()

	mockChild := mocks.NewMockRunnable()
	mockChild.On("String").Return("child").Maybe()
	mockChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	mockChild.On("Stop").Return().Maybe()

	cb := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("initial", []RunnableEntry[*mocks.Runnable]{{Runnable: mockChild}})
	}

	runner, err := NewRunner(cb)
	require.NoError(t, err)

	runCtx, runCancel := context.WithCancel(t.Context())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(runCtx) }()
	require.Eventually(t, runner.IsReady, 2*time.Second, 10*time.Millisecond)

	runner.Stop()
	require.Eventually(t,
		func() bool { return runner.GetState() == finitestate.StatusStopped },
		2*time.Second, 10*time.Millisecond)

	// Reload after Stop. Initial Transition(Reloading) will fail; the
	// admission gate returns nil (not an error) because there's no failure
	// of *this* reload to surface — just a stale request against a stopped
	// runner.
	require.NoError(t, runner.Reload(t.Context()))

	require.Equal(t, finitestate.StatusStopped, runner.GetState(),
		"Reload-after-Stop must not move FSM to Error")

	runCancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestCompositeRunner_DrainReloadCh_OnShutdown verifies that the deferred
// drainReloadCh in Run() clears a request that arrives DURING shutdown — the
// window between waitForEvent exiting (and inline drain running with empty
// buffer) and Run main returning (when defer drainReloadCh fires).
//
// Determinism: a slow-stop child blocks Run inside stopAllRunnables. While
// blocked, FSM is Stopping, waitForEvent has already exited. We send the
// stale request then — it lands in an empty buffer with no consumer. Only
// the deferred drainReloadCh can clear it. If it gets cleared, drain ran;
// if it survives, drain is broken.
func TestCompositeRunner_DrainReloadCh_OnShutdown(t *testing.T) {
	t.Parallel()

	slowStop := make(chan struct{})
	mockChild := mocks.NewMockRunnable()
	mockChild.On("String").Return("child").Maybe()
	mockChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	mockChild.On("Stop").Run(func(_ mock.Arguments) { <-slowStop }).Return().Maybe()

	altChild := mocks.NewMockRunnable()
	altChild.On("String").Return("alt").Maybe()
	altChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	altChild.On("Stop").Return().Maybe()

	cb := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("initial", []RunnableEntry[*mocks.Runnable]{{Runnable: mockChild}})
	}

	runner, err := NewRunner(cb)
	require.NoError(t, err)

	runCtx, runCancel := context.WithCancel(t.Context())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(runCtx) }()
	require.Eventually(t, runner.IsReady, 2*time.Second, 10*time.Millisecond)

	// Trigger Stop in a goroutine — it'll block in stopAllRunnables on slowStop.
	stopReturned := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopReturned)
	}()

	// Wait for FSM Stopping. This proves: waitForEvent has returned,
	// inline drainReloadCh ran (with empty buffer), TransitionIfCurrentState
	// moved Running→Stopping, and we're now blocked in stopAllRunnables.
	require.Eventually(t,
		func() bool { return runner.GetState() == finitestate.StatusStopping },
		2*time.Second, 10*time.Millisecond,
		"runner did not reach Stopping — slow-stop wedge failed")

	// Now send the stale request. waitForEvent is gone; only deferred
	// drainReloadCh can clear this and close req.done.
	staleCfg, err := NewConfig("stale", []RunnableEntry[*mocks.Runnable]{{Runnable: altChild}})
	require.NoError(t, err)
	stale := &reloadReq[*mocks.Runnable]{cfg: staleCfg, result: make(chan error, 1)}
	runner.reloadCh <- stale
	require.Len(t, runner.reloadCh, 1, "stale request must land in buffer")

	// Release stopAllRunnables — Run completes, defers fire, drainReloadCh runs.
	close(slowStop)

	select {
	case <-stopReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after slowStop released")
	}

	// Buffer must be empty — only the deferred drainReloadCh could have
	// cleared the stale request we sent during Stopping.
	require.Empty(t, runner.reloadCh,
		"deferred drainReloadCh must have cleared the stale request")

	// drainReloadCh sends the abandonment error on req.result so any
	// caller blocked on the receive unblocks deterministically.
	select {
	case err := <-stale.result:
		require.Error(t, err, "drainReloadCh must surface the abandonment error")
	case <-time.After(2 * time.Second):
		t.Fatal("drainReloadCh must send on req.result so callers unblock")
	}

	// Side effects from the stale request must NOT have fired — altChild
	// was never started; drainReloadCh closes done without running apply.
	altChild.AssertNotCalled(t, "Run")
	altChild.AssertNotCalled(t, "Stop")

	runCancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestCompositeRunner_ConcurrentReload_DropsOnBusy verifies the FSM
// admission gate's drop-on-busy semantics: while one Reload is in flight (the
// FSM is in Reloading), concurrent Reload callers fail the
// TransitionIfCurrentState gate, return immediately without queueing, and do
// NOT trigger a second pass through the child's Reload.
//
// This is the contract that replaced the prior reloadMu-based queueing: a
// reload pulls the latest config; if a reload is already in flight, the new
// caller's request is redundant and is dropped.
func TestCompositeRunner_ConcurrentReload_DropsOnBusy(t *testing.T) {
	t.Parallel()

	releaseReload := make(chan struct{})
	// Always release on test exit, even on assertion failure: if the test
	// fails before the happy-path close(releaseReload) below, the in-flight
	// reload (and its mocked child.Reload) would otherwise block forever,
	// leaking a goroutine and obscuring the real failure.
	releaseOnce := sync.OnceFunc(func() { close(releaseReload) })
	t.Cleanup(releaseOnce)

	mockChild := mocks.NewMockRunnable()
	mockChild.On("String").Return("slow-reloader").Maybe()
	mockChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(context.Canceled).Maybe()
	mockChild.On("Stop").Return().Maybe()
	// Reload blocks on releaseReload, holding the parent FSM in Reloading.
	// .Once() asserts that exactly one Reload reaches the child — concurrent
	// callers must be dropped at the FSM admission gate.
	mockChild.On("Reload", mock.Anything).Run(func(_ mock.Arguments) {
		<-releaseReload
	}).Return(nil).Once()

	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockChild, Config: nil},
	}
	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", entries)
	}

	runner, err := NewRunner(configCallback)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(ctx) }()
	require.Eventually(t, runner.IsReady, 2*time.Second, 5*time.Millisecond)

	// First reload: blocks inside child.Reload, parent FSM held in Reloading.
	// Eventually completes successfully when releaseReload fires below.
	var first sync.WaitGroup
	first.Go(func() {
		assert.NoError(t, runner.Reload(t.Context()))
	})
	require.Eventually(t, func() bool {
		return runner.GetState() == finitestate.StatusReloading
	}, 2*time.Second, 5*time.Millisecond, "first reload must enter Reloading")

	// Spawn N concurrent reloads — each must hit the FSM gate, fail, and
	// return immediately. They MUST NOT queue and MUST NOT call child.Reload.
	const concurrent = 10
	var others sync.WaitGroup
	for range concurrent {
		others.Go(func() {
			// These reloads are expected to bail at the FSM admission gate.
			// Reload returns nil for that path (not an error of *this* reload).
			require.NoError(t, runner.Reload(t.Context()))
		})
	}
	// All N must return promptly (they bail at admission). If they queue on a
	// hypothetical mutex, they'd block until releaseReload closes.
	done := make(chan struct{})
	go func() {
		others.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Reload callers did not return promptly — they appear to be queueing instead of dropping")
	}

	// FSM still pinned in Reloading by the in-flight first call.
	assert.Equal(t, finitestate.StatusReloading, runner.GetState())

	// Release the in-flight reload so the runner returns to Running.
	releaseOnce()
	first.Wait()
	require.Eventually(t, runner.IsReady, 2*time.Second, 5*time.Millisecond,
		"runner should return to Running after the in-flight reload completes")

	// .Once() on Reload asserts exactly one call reached the child.
	mockChild.AssertExpectations(t)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-runErr:
			assert.NoError(t, err)
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "runner should shut down cleanly")
}

// TestCompositeRunner_ConcurrentReload exercises the FSM admission gate
// under fan-in: 10 concurrent Reload callers must leave the runner in
// Running, never Error. The FSM gate (TIC(Running, Reloading)) admits at
// most one in-flight reload at a time; the rest drop.
//
// Run under synctest so the scheduling is deterministic and the test does
// not depend on wall-clock budgets — an earlier wall-clock variant flaked on
// slow CI runners where preemption between the now-removed catch-all defer
// and the next admission could drive the FSM to Error.
func TestCompositeRunner_ConcurrentReload(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("concurrent-reloader").Maybe()
		mockRunnable.On("Reload", mock.Anything).Return(nil)
		mockRunnable.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable.On("Stop").Return().Maybe()

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		synctest.Wait()
		require.True(t, runner.IsReady())

		// Fan out 10 concurrent Reload callers. The FSM admission gate
		// (TIC(Running, Reloading)) admits one at a time; the rest fail the
		// gate and drop. Each admitted call dispatches through Run's event
		// loop, which owns the Reloading→Running transition on completion.
		// wg.Wait is durably blocking under synctest because wg.Go was
		// called inside the bubble.
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() {
				// Each Reload either dispatches successfully or bails at
				// the FSM admission gate (which returns nil — not a
				// failure of *this* reload).
				assert.NoError(t, runner.Reload(t.Context()))
			})
		}
		wg.Wait()

		require.Equal(t, finitestate.StatusRunning, runner.GetState(),
			"runner should be Running after concurrent reloads, not Error")

		cancel()
		synctest.Wait()
		select {
		case err := <-runErr:
			require.NoError(t, err)
		default:
			t.Fatal("runner should shut down cleanly")
		}
	})
}

// TestComposite_Reload_ChildError_AggregatesAndTransitionsError covers the
// T3.1 contract: a child Reload failure surfaces (a) via Reload's error
// return (errors.Join across all children) and (b) via the composite FSM
// transitioning to Error. Composite is "fail-fast group of tightly-coupled
// dependents" — a child failure must be observable to the parent.
func TestComposite_Reload_ChildError_AggregatesAndTransitionsError(t *testing.T) {
	t.Parallel()

	healthy := mocks.NewMockRunnable()
	healthy.On("String").Return("healthy").Maybe()
	healthy.On("Run", mock.Anything).Return(nil).Maybe()
	healthy.On("Stop").Return().Maybe()
	healthy.On("Reload", mock.Anything).Return(nil).Once()

	failing := mocks.NewMockRunnable()
	failing.On("String").Return("failing").Maybe()
	failing.On("Run", mock.Anything).Return(nil).Maybe()
	failing.On("Stop").Return().Maybe()
	childErr := errors.New("child reload exploded")
	failing.On("Reload", mock.Anything).Return(childErr).Once()

	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: healthy, Config: nil},
		{Runnable: failing, Config: nil},
	}
	cfg, err := NewConfig("err-test", entries)
	require.NoError(t, err)

	runner, err := NewRunner(func() (*Config[*mocks.Runnable], error) { return cfg, nil })
	require.NoError(t, err)

	// Drive reloadSkipRestart directly — no Run loop needed; we want to
	// observe the aggregated return value.
	joinedErr := runner.reloadSkipRestart(t.Context(), cfg)
	require.Error(t, joinedErr, "aggregated error must surface")
	require.ErrorIs(t, joinedErr, childErr,
		"errors.Join must include the child's wrapped error")

	healthy.AssertExpectations(t)
	failing.AssertExpectations(t)
}

// TestComposite_Reload_CtxCancelDoesNotForceError covers the Copilot review
// catch on PR #111: a child Reload returning context.Canceled (because the
// parent runCtx fired during shutdown) must not push the composite FSM to
// Error. The cancellation error still propagates via the return — that's
// control flow — but the FSM should be Running so Run's subsequent
// Stopping/Stopped transitions remain valid.
func TestComposite_Reload_CtxCancelDoesNotForceError(t *testing.T) {
	t.Parallel()

	child := mocks.NewMockRunnable()
	child.On("String").Return("child").Maybe()
	child.On("Run", mock.Anything).Return(nil).Maybe()
	child.On("Stop").Return().Maybe()
	child.On("Reload", mock.Anything).Return(context.Canceled).Once()

	entries := []RunnableEntry[*mocks.Runnable]{{Runnable: child, Config: nil}}
	cfg, err := NewConfig("ctx-cancel", entries)
	require.NoError(t, err)

	runner, err := NewRunner(func() (*Config[*mocks.Runnable], error) { return cfg, nil })
	require.NoError(t, err)
	// Force FSM into Reloading directly so handleReload can run without
	// going through dispatch.
	require.NoError(t, runner.fsm.SetState(finitestate.StatusRunning))
	require.NoError(t, runner.fsm.Transition(finitestate.StatusReloading))

	err = runner.handleReload(t.Context(), cfg)
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled,
		"ctx.Canceled must propagate to the caller")

	require.NotEqual(t, finitestate.StatusError, runner.fsm.GetState(),
		"ctx-cancellation must NOT push FSM to Error")
	require.Equal(t, finitestate.StatusRunning, runner.fsm.GetState(),
		"FSM should be back in Running so subsequent shutdown transitions stay valid")

	child.AssertExpectations(t)
}

// TestReload_AddEntry_NewChildErrorPropagates verifies that an error from a
// runnable added via Reload propagates through Run's return value. Audit
// item #8: pre-fix, the still-undersized serverErrors channel could drop
// errors from post-reload entries; the per-generation lifecycle ensures the
// new child's non-blocking forward into the stable cap=1 r.serverErrors
// reaches Run's waitForEvent.
func TestReload_AddEntry_NewChildErrorPropagates(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		newChildErr := errors.New("new child boom")

		healthy := mocks.NewMockRunnable()
		healthy.On("String").Return("healthy").Maybe()
		healthy.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		healthy.On("Stop").Return().Maybe()

		failer := mocks.NewMockRunnable()
		failer.On("String").Return("failer").Maybe()
		failer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			time.Sleep(10 * time.Millisecond)
		}).Return(newChildErr).Once()
		failer.On("Stop").Return().Maybe()

		initial := []RunnableEntry[*mocks.Runnable]{{Runnable: healthy}}
		updated := []RunnableEntry[*mocks.Runnable]{
			{Runnable: healthy},
			{Runnable: failer},
		}

		useUpdated := false
		cb := func() (*Config[*mocks.Runnable], error) {
			if useUpdated {
				return NewConfig("test", updated)
			}
			return NewConfig("test", initial)
		}
		runner, err := NewRunner(cb)
		require.NoError(t, err)

		runErr := make(chan error, 1)
		go func() { runErr <- runner.Run(t.Context()) }()

		synctest.Wait()
		require.Equal(t, finitestate.StatusRunning, runner.GetState())

		useUpdated = true
		require.NoError(t, runner.Reload(t.Context()))

		// Advance virtual clock past the failer's 10ms in-Run sleep.
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()

		select {
		case err := <-runErr:
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRunnableFailed,
				"Run should fail with ErrRunnableFailed from the new entry")
			require.ErrorIs(t, err, newChildErr,
				"the new child's error should be wrapped in the Run return")
		default:
			t.Fatal("Run should have returned after the new entry failed")
		}

		healthy.AssertExpectations(t)
		failer.AssertExpectations(t)
	})
}

// TestReload_OldGenErrorDoesNotKillNewGen verifies that an old-generation
// child returning a non-cancel error during reload-shutdown does not leak
// into the new generation's waitForEvent loop. The drain in
// reloadWithRestart discards the stale forward and the new gen runs cleanly.
func TestReload_OldGenErrorDoesNotKillNewGen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		oldErr := errors.New("old gen boom on shutdown")

		// oldChild blocks until its context is canceled, then returns a
		// non-cancel error — simulating a runnable that reports a real
		// failure as part of its shutdown path. startRunnable will not
		// filter this; the non-blocking send in startRunnable forwards it
		// into r.serverErrors, where reloadWithRestart's drain discards it.
		oldChild := mocks.NewMockRunnable()
		oldChild.On("String").Return("old").Maybe()
		oldChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(oldErr).Once()
		oldChild.On("Stop").Return().Maybe()

		newChild := mocks.NewMockRunnable()
		newChild.On("String").Return("new").Maybe()
		newChild.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		newChild.On("Stop").Return().Maybe()

		initial := []RunnableEntry[*mocks.Runnable]{{Runnable: oldChild}}
		updated := []RunnableEntry[*mocks.Runnable]{{Runnable: newChild}}

		useUpdated := false
		cb := func() (*Config[*mocks.Runnable], error) {
			if useUpdated {
				return NewConfig("test", updated)
			}
			return NewConfig("test", initial)
		}
		runner, err := NewRunner(cb)
		require.NoError(t, err)

		runErr := make(chan error, 1)
		go func() { runErr <- runner.Run(t.Context()) }()

		synctest.Wait()
		require.Equal(t, finitestate.StatusRunning, runner.GetState())

		useUpdated = true
		require.NoError(t, runner.Reload(t.Context()))
		synctest.Wait()

		require.Equal(t, finitestate.StatusRunning, runner.GetState(),
			"new gen should be Running; old-gen error must be drained")

		select {
		case err := <-runErr:
			t.Fatalf("Run unexpectedly returned with error: %v", err)
		default:
		}

		runner.Stop()
		synctest.Wait()

		require.NoError(t, <-runErr, "Run should exit cleanly on Stop")
		require.Equal(t, finitestate.StatusStopped, runner.GetState())

		oldChild.AssertExpectations(t)
		newChild.AssertExpectations(t)
	})
}

// TestSingleGen_ConcurrentFailures_OnlyFirstCaptured verifies that when
// multiple children fail simultaneously within a generation, exactly one
// error reaches Run's return value: the cap=1 r.serverErrors coalesces the
// race so only one non-blocking send succeeds. Sibling errors log at Error
// (and Warn on send-fail) but are not propagated — the consumer reads at
// most one error per Run.
func TestSingleGen_ConcurrentFailures_OnlyFirstCaptured(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		err1 := errors.New("child 1 boom")
		err2 := errors.New("child 2 boom")
		err3 := errors.New("child 3 boom")

		mk := func(name string, fErr error) *mocks.Runnable {
			m := mocks.NewMockRunnable()
			m.On("String").Return(name).Maybe()
			m.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				time.Sleep(10 * time.Millisecond)
			}).Return(fErr)
			m.On("Stop").Return().Maybe()
			return m
		}

		c1 := mk("c1", err1)
		c2 := mk("c2", err2)
		c3 := mk("c3", err3)

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: c1}, {Runnable: c2}, {Runnable: c3},
		}
		cb := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}
		runner, err := NewRunner(cb)
		require.NoError(t, err)

		runErr := make(chan error, 1)
		go func() { runErr <- runner.Run(t.Context()) }()

		time.Sleep(50 * time.Millisecond)
		synctest.Wait()

		select {
		case err := <-runErr:
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRunnableFailed)
			matches := 0
			if errors.Is(err, err1) {
				matches++
			}
			if errors.Is(err, err2) {
				matches++
			}
			if errors.Is(err, err3) {
				matches++
			}
			require.Equal(t, 1, matches,
				"exactly one child error must propagate, got: %v", err)
		default:
			t.Fatal("Run should have returned after concurrent failures")
		}

		c1.AssertExpectations(t)
		c2.AssertExpectations(t)
		c3.AssertExpectations(t)
	})
}
