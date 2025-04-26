package composite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
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
		mockRunnable1.On("Reload").Once()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable1.On("Stop").Return(nil).Maybe() // Add explicit expectation for Stop

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2")
		mockRunnable2.On("Reload").Once()
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
			WithContext[*mocks.Runnable](ctx),
			WithLogHandler[*mocks.Runnable](handler),
		)
		require.NoError(t, err)
		assert.Equal(t, 0, callbackCalls, "first callback is after Runner.Run")

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel() // Use defer instead of t.Cleanup for immediate cancellation

		go func() {
			err := runner.Run(runCtx)
			require.NoError(t, err)
		}()

		require.Eventually(t, func() bool {
			return runner.IsRunning()
		}, 2*time.Second, 10*time.Millisecond)
		assert.Equal(t, 1, callbackCalls)

		runner.Reload()
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

		// Create initial entries
		initialEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create updated entries for reload
		updatedEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable3, Config: nil},
		}

		// Variable to track which set of entries to return
		useUpdatedEntries := false

		// Create config callback
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
		runner, err := NewRunner(
			configCallback,
			WithContext[*mocks.Runnable](ctx),
		)
		require.NoError(t, err)
		require.Equal(t, 0, callbackCalls)

		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			err := runner.Run(runCtx)
			require.NoError(t, err)
		}()

		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 1*time.Second, 10*time.Millisecond)

		// Switch to using updated entries
		useUpdatedEntries = true

		// Call Reload
		runner.Reload()

		// Verify updated config
		config := runner.getConfig()
		require.NotNil(t, config)
		assert.Len(t, config.Entries, 2)
		assert.Equal(t, mockRunnable1, config.Entries[0].Runnable)
		assert.Equal(t, mockRunnable3, config.Entries[1].Runnable)

		// Verify state cycle completed
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())

		runner.Stop()
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusStopped
		}, 1*time.Second, 10*time.Millisecond)

		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
		mockRunnable3.AssertExpectations(t)
	})

	t.Run("reload with updated configurations", func(t *testing.T) {
		// Setup mock runnables with proper blocking behavior
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload").Once()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			// Block until context is canceled - this mimics real runnable behavior
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe()
		mockRunnable1.On("Stop").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload").Once()
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
		runner, err := NewRunner(
			configCallback,
			WithContext[*mocks.Runnable](ctx),
		)
		require.NoError(t, err)
		assert.Equal(t, 0, callbackCalls)

		// Start the runner
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			err := runner.Run(runCtx)
			require.NoError(t, err)
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
		runner.Reload()

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
		// Setup mock runnables with proper blocking behavior
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1")
		mockRunnable1.On("Reload").Once()
		// Make Run properly block until context cancellation
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Once()
		mockRunnable1.On("Stop").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2")
		mockRunnable2.On("Reload").Once()
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
		runner, err := NewRunner(
			configCallback,
			WithContext[*mocks.Runnable](ctx),
		)
		require.NoError(t, err)
		require.Equal(t, 0, callbackCalls, "Callback should not be called during creation")

		// Start the runner
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			err := runner.Run(runCtx)
			require.NoError(t, err)
		}()

		// Verify runner reaches Running state
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 2*time.Second, 10*time.Millisecond, "Runner should transition to Running state")

		// Verify initial config loaded
		assert.Equal(t, 1, callbackCalls, "Config callback should be called once during startup")

		// Call Reload with the same configuration
		runner.Reload()

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

		// Create runner with proper context
		ctx := t.Context()
		runner, err := NewRunner(
			configCallback,
			WithContext[*MockReloadableWithConfig](ctx),
		)
		require.NoError(t, err)
		assert.Equal(t, 0, callbackCalls)

		// Start the runner
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			err := runner.Run(runCtx)
			require.NoError(t, err)
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
		runner.Reload()

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
		// Setup mock FSM with specific error behavior
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).
			Return(errors.New("transition error")).
			Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(nil).Once()
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()

		// Create mock runnables to make test more realistic
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1").Maybe()

		// Create entries for a valid config
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		// Create config callback that returns a valid config
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock that will fail transition
		runner.fsm = mockFSM

		// Call Reload - should handle the transition error
		runner.Reload()

		// Verify FSM methods were called as expected
		mockFSM.AssertExpectations(t)

		// Verify we're in error state
		assert.Equal(t, finitestate.StatusError, mockFSM.GetState())
	})

	t.Run("config callback error during reload", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil).Once()
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
		runner.Reload()

		// Verify FSM methods were called as expected
		mockFSM.AssertExpectations(t)

		// Verify we're in error state
		assert.Equal(t, finitestate.StatusError, mockFSM.GetState())
	})

	t.Run("config callback returns nil config", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil).Once()
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
		runner.Reload()

		// Verify FSM methods were called as expected
		mockFSM.AssertExpectations(t)
		assert.Equal(t, 1, callCount, "Config callback should be called once")
		assert.Equal(t, finitestate.StatusError, mockFSM.GetState(), "Should be in error state")
	})

	t.Run("reloadable runnable fails", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil).Once()
		// Do NOT expect SetState(StatusError) since the panic will interrupt execution
		mockFSM.On("GetState").Return(finitestate.StatusReloading).Maybe()

		// Setup mock runnable that fails on Reload
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1").Maybe()
		mockRunnable.On("Reload").
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
			runner.Reload()
		}()

		// Verify that the panic was propagated (not handled internally)
		assert.True(t, didPanic, "Runner.Reload() should propagate panics")
		assert.Contains(t, panicMsg, "reload failure",
			"Panic message should contain the original panic reason")

		// Since execution was interrupted by panic, only the first mock expectation should be met
		mockFSM.AssertCalled(t, "Transition", finitestate.StatusReloading)

		// Verify our mock expectations
		mockRunnable.AssertExpectations(t)
	})

	t.Run("reload with ReloadableWithConfig that panics", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil).Once()
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
			runner.Reload()
		}()

		// Verify that the panic was propagated (not handled internally)
		assert.True(t, didPanic, "Runner.Reload() should propagate panics")
		assert.Contains(t, panicMsg, "intentional panic in ReloadWithConfig",
			"Panic message should contain the original panic reason")

		// Since execution was interrupted by panic, only the first mock expectation should be met
		mockFSM.AssertCalled(t, "Transition", finitestate.StatusReloading)

		// We shouldn't have reached the error state since panic interrupted execution
		mockReloadable.AssertExpectations(t)
	})

	t.Run("reloadable runnable fails with Panic", func(t *testing.T) {
		// Setup mock FSM with expected transitions
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil).Once()
		// We don't expect SetState to be called since panic will interrupt execution
		mockFSM.On("GetState").Return(finitestate.StatusReloading).Maybe()

		// Setup mock runnable that fails on Reload using Panic() instead of Run()
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable-panic").Maybe()
		mockRunnable.On("Reload").Panic("intentional panic in Reload").Once()

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
			runner.Reload()
		}()

		// The runner does NOT handle panics internally, so we should see one
		assert.True(t, didPanic, "Runner.Reload() should propagate panics")
		assert.Contains(t, panicMsg, "intentional panic in Reload",
			"Panic message should contain the original panic reason")

		// Since execution was interrupted by panic, only the first mock expectation should be met
		mockFSM.AssertCalled(t, "Transition", finitestate.StatusReloading)
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

	// Create a mock for MockRunnableWithStatable to use in tests
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
		mockRunnable1.On("Reload").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload").Once()

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
		logger := runner.logger.WithGroup("test")
		runner.reloadConfig(logger, config)

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

		// Call reloadConfig directly
		logger := runner.logger.WithGroup("test")
		runner.reloadConfig(logger, config)

		// Verify ReloadWithConfig was called with correct configs
		mockReloadable1.AssertExpectations(t)
		mockReloadable2.AssertExpectations(t)
	})

	t.Run("with mixed interface implementations", func(t *testing.T) {
		// Setup mock that implements standard Reloadable
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Reload").Once()

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

		// Call reloadConfig on both runners
		logger := runner1.logger.WithGroup("test")
		runner1.reloadConfig(logger, config1)
		runner2.reloadConfig(logger, config2)

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
		// Set DelayRun to 0 to avoid timing issues
		mockRunnable3.DelayRun = 0
		mockRunnable3.On("String").Return("runnable3").Maybe()
		// Allow any number of Run calls for resilience
		mockRunnable3.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable4 := mocks.NewMockRunnable()
		// Set DelayRun to 0 to avoid timing issues
		mockRunnable4.DelayRun = 0
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

		// Create runner with context to use during boot
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create callback that initially returns oldEntries
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback, WithContext[*mocks.Runnable](ctx))
		require.NoError(t, err)

		// Make sure initial config is loaded
		initialConfigLoaded := runner.getConfig()
		assert.NotNil(t, initialConfigLoaded)
		assert.Equal(t, 2, len(initialConfigLoaded.Entries))

		// Set runCtx (normally done by Run)
		runCtx := t.Context()
		runner.runnablesMu.Lock()
		runner.runCtx = runCtx
		runner.runnablesMu.Unlock()

		// Call reloadMembershipChanged directly
		err = runner.reloadMembershipChanged(newConfig)
		require.NoError(t, err)

		// Verify config was updated
		updatedConfig := runner.getConfig()
		require.NotNil(t, updatedConfig)
		assert.Equal(t, 2, len(updatedConfig.Entries))
		assert.Equal(t, mockRunnable3, updatedConfig.Entries[0].Runnable)
		assert.Equal(t, mockRunnable4, updatedConfig.Entries[1].Runnable)

		// Verify mock expectations - disable t.Parallel() and wait a moment to make test more reliable
		time.Sleep(50 * time.Millisecond)
		mockRunnable1.AssertExpectations(t)
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

		// Set runCtx (normally done by Run)
		runCtx := t.Context()
		runner.runnablesMu.Lock()
		runner.runCtx = runCtx
		runner.runnablesMu.Unlock()

		// Call reloadMembershipChanged
		// This should fail because getConfig() returns nil in stopRunnables
		err = runner.reloadMembershipChanged(newConfig)

		// Verify error
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConfigMissing)
	})

	t.Run("handles boot error", func(t *testing.T) {
		// Setup mock runnables for old config
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Stop").Maybe()

		// Create initial entries with no runnables
		oldEntries := []RunnableEntry[*mocks.Runnable]{}

		// Create new entries with no runnables to force boot error
		newEntries := []RunnableEntry[*mocks.Runnable]{}

		initialConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Create context for the runner
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create callback that initially returns oldEntries
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback,
			WithContext[*mocks.Runnable](ctx),
		)
		require.NoError(t, err)

		// Make sure initial config is loaded
		runner.currentConfig.Store(initialConfig)

		// Set runCtx (normally done by Run)
		runCtx := t.Context()
		runner.runnablesMu.Lock()
		runner.runCtx = runCtx
		runner.runnablesMu.Unlock()

		// Call reloadMembershipChanged directly
		// This should fail because the new config has no entries
		err = runner.reloadMembershipChanged(newConfig)

		// Verify error (ErrNoRunnables is returned by boot when no runnables exist)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoRunnables)
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
		// Disable parallel to avoid interactions with other tests
		// t.Parallel() - removing this to avoid test interactions

		// Setup initial runnables with consistent expectations
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Stop").Return(nil).Maybe() // Use Maybe() for more resilience
		mockRunnable1.On("Reload").Maybe()
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe() // Make sure Run blocks on context

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Stop").Return(nil).Maybe() // Use Maybe() for more resilience
		mockRunnable2.On("Reload").Maybe()
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe() // Make sure Run blocks on context

		// Setup completely new runnables for reload with consistent expectations
		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("runnable3").Maybe()
		mockRunnable3.On("Stop").Return(nil).Maybe() // Add expectation for Stop
		mockRunnable3.On("Reload").Maybe()
		mockRunnable3.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe() // Make Run block on context

		mockRunnable4 := mocks.NewMockRunnable()
		mockRunnable4.On("String").Return("runnable4").Maybe()
		mockRunnable4.On("Stop").Return(nil).Maybe() // Add expectation for Stop
		mockRunnable4.On("Reload").Maybe()
		mockRunnable4.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(context.Canceled).Maybe() // Make Run block on context

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

		ctx := context.Background() // Use a clean context instead of t.Context()
		runner, err := NewRunner(configCallback,
			WithContext[*mocks.Runnable](ctx),
		)
		require.NoError(t, err)

		// Use a cancelable context and ensure cleanup with defer
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel() // Ensure context is always canceled at the end of test

		errCh := make(chan error, 1)
		go func() {
			errCh <- runner.Run(runCtx)
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
		runner.Reload()

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
		cancel()

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
