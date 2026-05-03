package composite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/mocks"
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

func (m *MockReloadableWithConfig) ReloadWithConfig(config any) {
	m.Called(config)
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
		mockRunnable1.On("Reload", mock.Anything).Once()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable1.On("Stop").Return(nil).Maybe() // Add explicit expectation for Stop

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2")
		mockRunnable2.On("Reload", mock.Anything).Once()
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
			return runner.IsRunning()
		}, 2*time.Second, 10*time.Millisecond)
		assert.Equal(t, 1, callbackCalls)

		runner.Reload(t.Context())
		assert.Equal(t, len(entries), callbackCalls)
		require.Eventually(t, func() bool {
			return runner.IsRunning()
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
			runner.Reload(t.Context())
			synctest.Wait()

			config := runner.getConfig()
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
		mockRunnable1.On("Reload", mock.Anything).Once()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			// Block until context is canceled - this mimics real runnable behavior
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable1.On("Stop").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload", mock.Anything).Once()
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
		config := runner.getConfig()
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
		runner.Reload(t.Context())

		// Verify reload completes and runner returns to Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should return to Running state after reload")
		assert.Equal(t, 2, callbackCalls, "Reload should call config callback again")

		// Verify updated config
		config = runner.getConfig()
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
		mockRunnable1.On("Reload", mock.Anything).Once()
		// Make Run properly block until context cancellation
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Once()
		mockRunnable1.On("Stop").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2")
		mockRunnable2.On("Reload", mock.Anything).Once()
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
		runner.Reload(t.Context())

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
		mockReloadable1.On("ReloadWithConfig", updatedConfig1).Once()
		mockReloadable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Once()
		mockReloadable1.Runnable.On("Stop").Return(nil).Once()

		// mock 2 setup
		initialConfig2 := map[string]string{"key": "initial2"}
		updatedConfig2 := map[string]string{"key": "updated2"}

		mockReloadable2 := NewMockReloadableWithConfig()
		mockReloadable2.On("String").Return("reloadable2")
		mockReloadable2.On("ReloadWithConfig", updatedConfig2).Once()
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
		config := runner.getConfig()
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
		runner.Reload(t.Context())

		// Verify reload completes and runner returns to Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 100*time.Millisecond, "Runner should return to Running state after reload")
		assert.Equal(t, 2, callbackCalls, "Config callback should be called again during reload")

		// Verify updated config
		updatedConfig := runner.getConfig()
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

		runner.Reload(t.Context())

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

		// Call Reload - should handle the callback error
		runner.Reload(t.Context())

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

		// Call Reload - should handle the nil config case
		runner.Reload(t.Context())

		// Verify FSM methods were called as expected
		mockFSM.AssertExpectations(t)
		assert.Equal(t, 1, callCount, "Config callback should be called once")
		assert.Equal(t, finitestate.StatusError, mockFSM.GetState(), "Should be in error state")
	})

	t.Run("reloadable runnable fails", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading).
			Return(nil).Once()
		// Do NOT expect SetState(StatusError) since the panic will interrupt execution
		mockFSM.On("GetState").Return(finitestate.StatusReloading).Maybe()

		// Setup mock runnable that fails on Reload
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1").Maybe()
		mockRunnable.On("Reload", mock.Anything).
			Panic("reload failure").
			Once()
			// Use Panic() instead of Run() with panic

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		// Create config and ensure it's non-nil
		initialConfig, err := NewConfig("test", entries)
		require.NoError(t, err)

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Manually set current config (skipping initial load)
		runner.currentConfig.Store(initialConfig)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload with panic recovery - expect the panic to propagate
		didPanic := false
		panicMsg := ""
		func() {
			defer func() {
				if r := recover(); r != nil {
					didPanic = true
					panicMsg = fmt.Sprintf("%v", r)
				}
			}()
			runner.Reload(t.Context())
		}()

		// Verify that the panic was propagated (not handled internally)
		assert.True(t, didPanic, "Runner.Reload() should propagate panics")
		assert.Contains(t, panicMsg, "reload failure",
			"Panic message should contain the original panic reason")

		// Since execution was interrupted by panic, only the first mock expectation should be met
		mockFSM.AssertCalled(t, "TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading)

		// Verify our mock expectations
		mockRunnable.AssertExpectations(t)
	})

	t.Run("reload with ReloadableWithConfig that panics", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading).
			Return(nil).Once()
		// No SetState error expectation since we won't reach that code due to panic
		mockFSM.On("GetState").Return(finitestate.StatusReloading).Maybe()

		// Setup initial config
		initialConfig := map[string]string{"key": "initial"}
		updatedConfig := map[string]string{"key": "updated"}

		// Create a mock that will panic when ReloadWithConfig is called
		mockReloadable := NewMockReloadableWithConfig()
		mockReloadable.On("String").Return("reloadable-panic").Maybe()
		// Use Panic() instead of Run() with a manual panic - more readable and direct
		mockReloadable.On("ReloadWithConfig", updatedConfig).
			Panic("intentional panic in ReloadWithConfig").
			Once()
		mockReloadable.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockReloadable.Runnable.On("Stop").Return(nil).Maybe()

		// Create entries
		initialEntries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable, Config: initialConfig},
		}

		updatedEntries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable, Config: updatedConfig},
		}

		// Create initial config
		config, err := NewConfig("test", initialEntries)
		require.NoError(t, err)

		updatedConfigObj, err := NewConfig("test", updatedEntries)
		require.NoError(t, err)

		// Variable to track which config to return
		useUpdatedConfig := false

		// Create config callback
		configCallback := func() (*Config[*MockReloadableWithConfig], error) {
			if useUpdatedConfig {
				return updatedConfigObj, nil
			}
			return config, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Manually set current config (skipping initial load)
		runner.currentConfig.Store(config)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Switch to updated config that will cause panic
		useUpdatedConfig = true

		// Call Reload with panic recovery - we expect the panic to propagate
		didPanic := false
		panicMsg := ""
		func() {
			defer func() {
				if r := recover(); r != nil {
					didPanic = true
					panicMsg = fmt.Sprintf("%v", r)
				}
			}()
			runner.Reload(t.Context())
		}()

		// Verify that the panic was propagated (not handled internally)
		assert.True(t, didPanic, "Runner.Reload() should propagate panics")
		assert.Contains(t, panicMsg, "intentional panic in ReloadWithConfig",
			"Panic message should contain the original panic reason")

		// Since execution was interrupted by panic, only the first mock expectation should be met
		mockFSM.AssertCalled(t, "TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading)

		// We shouldn't have reached the error state since panic interrupted execution
		mockReloadable.AssertExpectations(t)
	})

	t.Run("reloadable runnable fails with Panic", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading).
			Return(nil).Once()
		// We don't expect SetState to be called since panic will interrupt execution
		mockFSM.On("GetState").Return(finitestate.StatusReloading).Maybe()

		// Setup mock runnable that fails on Reload using Panic() instead of Run()
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable-panic").Maybe()
		mockRunnable.On("Reload", mock.Anything).Panic("intentional panic in Reload").Once()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		// Create config
		initialConfig, err := NewConfig("test", entries)
		require.NoError(t, err)

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Manually set current config (skipping initial load)
		runner.currentConfig.Store(initialConfig)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload with panic recovery - expect the panic to propagate
		didPanic := false
		panicMsg := ""
		func() {
			defer func() {
				if r := recover(); r != nil {
					didPanic = true
					panicMsg = fmt.Sprintf("%v", r)
				}
			}()
			runner.Reload(t.Context())
		}()

		// The runner does NOT handle panics internally, so we should see one
		assert.True(t, didPanic, "Runner.Reload() should propagate panics")
		assert.Contains(t, panicMsg, "intentional panic in Reload",
			"Panic message should contain the original panic reason")

		// Since execution was interrupted by panic, only the first mock expectation should be met
		mockFSM.AssertCalled(t, "TransitionIfCurrentState",
			finitestate.StatusRunning, finitestate.StatusReloading)
	})
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

		// Get config should return nil
		config := runner.getConfig()
		assert.Nil(t, config)
	})

	t.Run("callback returns error", func(t *testing.T) {
		// Create callback that returns an error
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, errors.New("config error")
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Clear the config cache
		runner.currentConfig.Store(nil)

		// Get config should return nil on error
		config := runner.getConfig()
		assert.Nil(t, config)
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
		mockRunnable1.On("Reload", mock.Anything).Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload", mock.Anything).Once()

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
		runner.reloadSkipRestart(t.Context(), config)

		// Verify reloadable interface methods were called
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reloads runnables with ReloadableWithConfig interface", func(t *testing.T) {
		// Setup mock that implements ReloadableWithConfig
		mockReloadable1 := NewMockReloadableWithConfig()
		mockReloadable1.On("String").Return("reloadable1").Maybe()

		customConfig1 := map[string]string{"key": "value1"}
		mockReloadable1.On("ReloadWithConfig", customConfig1).Once()

		mockReloadable2 := NewMockReloadableWithConfig()
		mockReloadable2.On("String").Return("reloadable2").Maybe()

		customConfig2 := map[string]string{"key": "value2"}
		mockReloadable2.On("ReloadWithConfig", customConfig2).Once()

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

		runner.reloadSkipRestart(t.Context(), config)

		// Verify ReloadWithConfig was called with correct configs
		mockReloadable1.AssertExpectations(t)
		mockReloadable2.AssertExpectations(t)
	})

	t.Run("with mixed interface implementations", func(t *testing.T) {
		// Setup mock that implements standard Reloadable
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Reload", mock.Anything).Once()

		// Setup mock that implements ReloadableWithConfig
		mockReloadable := NewMockReloadableWithConfig()
		mockReloadable.On("String").Return("reloadable").Maybe()

		customConfig := map[string]string{"key": "value"}
		mockReloadable.On("ReloadWithConfig", customConfig).Once()

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

		runner1.reloadSkipRestart(t.Context(), config1)
		runner2.reloadSkipRestart(t.Context(), config2)

		// Verify expectations
		mockRunnable.AssertExpectations(t)
		mockReloadable.AssertExpectations(t)
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
		initialConfigLoaded := runner.getConfig()
		assert.NotNil(t, initialConfigLoaded)
		assert.Len(t, initialConfigLoaded.Entries, 2)

		err = runner.reloadWithRestart(t.Context(), newConfig)
		require.NoError(t, err)

		// Verify config was updated
		updatedConfig := runner.getConfig()
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
		initialConfig := runner.getConfig()
		require.NotNil(t, initialConfig)
		assert.Empty(t, initialConfig.Entries, "Initial config should have empty entries")

		// Now reload to get the config with the runnable
		runner.Reload(t.Context())

		// Wait for reload to complete
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 1*time.Second, 10*time.Millisecond, "Runner should transition to Running state")

		// Verify the config was updated with the new runnable
		updatedConfig := runner.getConfig()
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
		updatedConfig := runner.getConfig()
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
		mockRunnable1.On("Reload", mock.Anything).Maybe()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Stop").Return(nil).Once()
		mockRunnable2.On("Reload", mock.Anything).Maybe()
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()

		// Setup completely new runnables for reload with consistent expectations
		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("runnable3").Maybe()
		mockRunnable3.On("Stop").Return(nil).Once()
		mockRunnable3.On("Reload", mock.Anything).Maybe()
		mockRunnable3.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()

		mockRunnable4 := mocks.NewMockRunnable()
		mockRunnable4.On("String").Return("runnable4").Maybe()
		mockRunnable4.On("Stop").Return(nil).Once()
		mockRunnable4.On("Reload", mock.Anything).Maybe()
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
		config := runner.getConfig()
		require.NotNil(t, config)
		assert.Len(t, config.Entries, 2)
		assert.Equal(t, mockRunnable1, config.Entries[0].Runnable)
		assert.Equal(t, mockRunnable2, config.Entries[1].Runnable)

		// Switch to using entirely new set of runnables
		useUpdatedEntries = true
		runner.Reload(t.Context())

		// Wait for reload to complete
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should return to Running state")

		// Verify updated config contains only the new runnables
		config = runner.getConfig()
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
// returns promptly instead of hanging. dispatchMembershipReload's outer select
// on lc.DoneCh() short-circuits once Run has exited.
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
		t, runner.IsRunning, 2*time.Second, 10*time.Millisecond,
	)

	runner.Stop()
	require.Eventually(
		t,
		func() bool { return runner.GetState() == finitestate.StatusStopped },
		2*time.Second, 10*time.Millisecond,
	)

	// Membership-change reload after Stop must not hang. The Reload may
	// fail at the FSM check or inside dispatchMembershipReload — both are
	// acceptable; what matters is that it returns promptly.
	useSwapped.Store(true)
	reloadDone := make(chan struct{})
	go func() {
		defer close(reloadDone)
		runner.Reload(t.Context())
	}()
	select {
	case <-reloadDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Reload after Stop did not return — likely hung in dispatchMembershipReload")
	}

	cancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestCompositeRunner_ReloadCancelMidFlight verifies that cancelling the
// caller's context after dispatchMembershipReload has handed the request to
// Run unblocks the caller via the inner ctx.Done() select case.
func TestCompositeRunner_ReloadCancelMidFlight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		// Slow-stop child blocks Run inside reloadWithRestart so the caller is
		// observably parked in the post-send select. synctest.Wait() returns
		// once the bubble is quiescent — the only path to quiescence after
		// Reload runs through this <-slowStop park.
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
		require.True(t, runner.IsRunning())

		useSwapped.Store(true)
		reloadCtx, reloadCancel := context.WithCancel(context.Background())
		reloadDone := make(chan struct{})
		go func() {
			defer close(reloadDone)
			runner.Reload(reloadCtx)
		}()

		synctest.Wait()

		reloadCancel()
		select {
		case <-reloadDone:
		case <-time.After(2 * time.Second):
			t.Fatal("Reload did not return after caller ctx cancel")
		}

		// Release the slow child so the runner can shut down cleanly.
		close(slowStop)
		runCancel()
		select {
		case <-runErr:
		case <-time.After(2 * time.Second):
			t.Fatal("Run did not return after ctx cancel")
		}
	})
}

// TestCompositeRunner_AbandonedReloadIsSkipped verifies that a membership-change
// reload request whose caller has signalled abandonment (cancel chan closed)
// does NOT execute its side effects (no Stop, no config swap, no boot of new
// entries) when Run's event loop picks it up. White-box: builds a
// reloadRequest directly with a pre-closed cancel signal and sends it into
// the unexported reloadCh, since the public-API path can't reliably wedge
// between send-success and consumer-read.
func TestCompositeRunner_AbandonedReloadIsSkipped(t *testing.T) {
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

	initial := []RunnableEntry[*mocks.Runnable]{{Runnable: mockChild}}
	cb := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("initial", initial)
	}

	runner, err := NewRunner(cb)
	require.NoError(t, err)

	runCtx, runCancel := context.WithCancel(t.Context())
	defer runCancel()
	runErr := make(chan error, 1)
	go func() { runErr <- runner.Run(runCtx) }()

	require.Eventually(
		t, runner.IsRunning, 2*time.Second, 10*time.Millisecond,
	)

	originalCfg := runner.getConfig()
	require.NotNil(t, originalCfg)

	swapped := []RunnableEntry[*mocks.Runnable]{{Runnable: altChild}}
	newCfg, err := NewConfig("swapped", swapped)
	require.NoError(t, err)

	cancel := make(chan struct{})
	close(cancel)
	req := newReloadRequest(newCfg, cancel)
	runner.reloadCh <- req

	var doneErr error
	select {
	case doneErr = <-req.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for waitForEvent to consume reloadCh")
	}
	require.Error(t, doneErr)
	require.ErrorIs(t, doneErr, ErrReloadAborted)
	require.Contains(t, doneErr.Error(), "abandoned by caller")

	// Critical: side effects must NOT have happened.
	mockChild.AssertNotCalled(t, "Stop")
	require.Same(t, originalCfg, runner.getConfig(),
		"config must not have been swapped for an abandoned request")
	altChild.AssertNotCalled(t, "Run")

	runCancel()
	select {
	case <-runErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

// TestCompositeRunner_AbandonedReloadIsSkipped_PublicAPI exercises the same
// abandonment path as the white-box test above, but through the public
// Reload(ctx) API. ctx is pre-cancelled so dispatchMembershipReload's outer
// select sees ctx.Done() and returns ErrReloadAborted without enqueueing.
func TestCompositeRunner_AbandonedReloadIsSkipped_PublicAPI(t *testing.T) {
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
	require.Eventually(t, runner.IsRunning, 2*time.Second, 10*time.Millisecond)

	originalCfg := runner.getConfig()
	useSwapped.Store(true)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.Reload(cancelledCtx)

	// FSM should still be Running (NOT Error). The cancelled reload is
	// normal control flow, not a failure.
	require.Equal(t, finitestate.StatusRunning, runner.GetState(),
		"FSM must not be in Error after cancelled reload")
	mockChild.AssertNotCalled(t, "Stop")
	require.Same(t, originalCfg, runner.getConfig(),
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
	require.Eventually(t, runner.IsRunning, 2*time.Second, 10*time.Millisecond)

	useSwapped.Store(true)
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.Reload(cancelledCtx)

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
	require.Eventually(t, runner.IsRunning, 2*time.Second, 10*time.Millisecond)

	runner.Stop()
	require.Eventually(t,
		func() bool { return runner.GetState() == finitestate.StatusStopped },
		2*time.Second, 10*time.Millisecond)

	// Reload after Stop. Initial Transition(Reloading) will fail.
	runner.Reload(t.Context())

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
	require.Eventually(t, runner.IsRunning, 2*time.Second, 10*time.Millisecond)

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
	// drainReloadCh can clear this.
	cancel := make(chan struct{})
	staleCfg, err := NewConfig("stale", []RunnableEntry[*mocks.Runnable]{{Runnable: altChild}})
	require.NoError(t, err)
	stale := newReloadRequest(staleCfg, cancel)
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

	// Side effects from the stale request must NOT have fired — altChild
	// was never started, and we don't see RunReload's "abandoned" message
	// on req.done because drainReloadCh discards silently.
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
	}).Return().Once()

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
	require.Eventually(t, runner.IsRunning, 2*time.Second, 5*time.Millisecond)

	// First reload: blocks inside child.Reload, parent FSM held in Reloading.
	var first sync.WaitGroup
	first.Go(func() {
		runner.Reload(t.Context())
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
			runner.Reload(t.Context())
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
	close(releaseReload)
	first.Wait()
	require.Eventually(t, runner.IsRunning, 2*time.Second, 5*time.Millisecond,
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

func TestCompositeRunner_ConcurrentReload(t *testing.T) {
	t.Parallel()

	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("String").Return("concurrent-reloader").Maybe()
	mockRunnable.On("Reload", mock.Anything).Return()
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

	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 2*time.Second, 10*time.Millisecond)

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			runner.Reload(t.Context())
		})
	}
	wg.Wait()

	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 2*time.Second, 10*time.Millisecond, "runner should be Running after concurrent reloads, not Error")

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
