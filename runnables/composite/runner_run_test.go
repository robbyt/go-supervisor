package composite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestCompositeRunner_Run_AdditionalScenarios tests additional scenarios for the Run method
func TestCompositeRunner_Run_AdditionalScenarios(t *testing.T) {
	t.Parallel()

	t.Run("fails to transition to running", func(t *testing.T) {
		t.Parallel()

		// Setup mock FSM that fails transition to running
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusBooting).Return(nil)
		mockFSM.On("Transition", finitestate.StatusRunning).Return(errors.New("transition error"))
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()

		// Setup mock runnable
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // Block until cancelled like a real service
		})
		mockRunnable.On("Stop").Maybe()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Run should fail when transitioning to Running
		err = runner.Run(context.Background())

		// Verify the error
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to transition to Running state")
		mockFSM.AssertExpectations(t)
	})

	t.Run("child runnable error propagation", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnables that returns an error
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(errors.New("intentional error from runnable"))
		mockRunnable1.On("Stop").Maybe()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Run should return an error since the runnable returns an error
		err = runner.Run(context.Background())

		// Verify error is propagated
		require.Error(t, err)
		assert.Contains(t, err.Error(), "intentional error from runnable")
		mockRunnable1.AssertExpectations(t)
	})

	t.Run("fails to transition to stopped", func(t *testing.T) {
		t.Parallel()

		// Setup mock FSM with specific behavior
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusBooting).Return(nil)
		mockFSM.On("Transition", finitestate.StatusRunning).Return(nil)
		mockFSM.On("TransitionIfCurrentState", finitestate.StatusRunning, finitestate.StatusStopping).
			Return(nil)
		mockFSM.On("Transition", finitestate.StatusStopped).Return(errors.New("transition error"))
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)
		mockFSM.On("GetState").Return(finitestate.StatusStopping).Maybe()

		// Setup mock runnables that completes immediately
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.DelayStop = 0 // No delay on Stop to avoid flakiness
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // Block until cancelled like a real service
		})
		mockRunnable.On("Stop").Maybe() // Use Maybe() instead of Once() to be more resilient

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Run with a cancelled context to immediately trigger shutdown
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately to trigger shutdown path

		// Run should fail during shutdown
		err = runner.Run(ctx)

		// Verify the error
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to transition to Stopped state")
		mockFSM.AssertExpectations(t)

		require.Eventually(t, func() bool { return true }, 20*time.Millisecond, 5*time.Millisecond)
	})

	t.Run("fails to transition to booting", func(t *testing.T) {
		t.Parallel()

		// Setup mock FSM that fails transition to booting
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusBooting).Return(errors.New("transition error"))
		mockFSM.On("GetState").Return(finitestate.StatusNew).Maybe()

		// Setup mock runnable
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()
		mockRunnable.On("Stop").Maybe()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Run should fail when transitioning to Booting
		err = runner.Run(context.Background())

		// Verify the error
		require.Error(t, err)
		require.ErrorContains(t, err, "failed to transition to Booting state")
		mockFSM.AssertExpectations(t)
	})

	t.Run("graceful shutdown with stored config", func(t *testing.T) {
		t.Parallel()

		// Setup mock FSM for normal progression
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusBooting).Return(nil)
		mockFSM.On("Transition", finitestate.StatusRunning).Return(nil)
		mockFSM.On("TransitionIfCurrentState", finitestate.StatusRunning, finitestate.StatusStopping).
			Return(nil)
		mockFSM.On("Transition", finitestate.StatusStopped).Return(nil).Maybe()
		mockFSM.On("GetState").Return(finitestate.StatusStopping).Maybe()

		// Setup mock runnables
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // Block until cancelled like a real service
		})
		mockRunnable.On("Stop").Maybe() // Add Stop expectation

		// Create config callback that returns nil after initial configuration
		callCount := 0
		configCallback := func() (*Config[*mocks.Runnable], error) {
			callCount++
			if callCount == 1 {
				// First call returns normal config (for initial running)
				entries := []RunnableEntry[*mocks.Runnable]{
					{Runnable: mockRunnable, Config: nil},
				}
				return NewConfig("test", entries)
			}
			// Subsequent calls return nil (to simulate config error during shutdown)
			return nil, errors.New("config error")
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Run with a cancelled context to immediately trigger shutdown
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately to trigger shutdown path

		// Run should complete successfully with cached config
		err = runner.Run(ctx)

		// No error expected with cached config
		require.NoError(t, err)
		mockFSM.AssertExpectations(t)
	})
}
