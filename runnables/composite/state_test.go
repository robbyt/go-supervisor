package composite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCompositeRunner_Run(t *testing.T) {
	t.Parallel()

	t.Run("successful run and graceful shutdown", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil)
		mockRunnable1.On("Stop").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Run", mock.Anything).Return(nil)
		mockRunnable2.On("Stop").Once()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Run in a goroutine and cancel after a short time
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)

		go func() {
			errCh <- runner.Run(ctx)
		}()

		// Wait for states to transition to Running
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 500*time.Millisecond, 10*time.Millisecond, "Runner should transition to Running state")
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())

		// Cancel the context to stop the runner
		cancel()

		// Wait for Run to complete
		var runErr error
		select {
		case runErr = <-errCh:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timeout waiting for Run to complete")
		}

		assert.NoError(t, runErr)
		assert.Equal(t, finitestate.StatusStopped, runner.GetState())
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("runnable fails during execution", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()

		var capturedRunner *Runner[*mocks.Runnable]
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			// Create a goroutine that will send an error to the serverErrors channel
			go func() {
				time.Sleep(50 * time.Millisecond)
				capturedRunner.serverErrors <- errors.New("runnable error")
			}()

			// Wait until context is cancelled
			<-args.Get(0).(context.Context).Done()
		}).Return(nil)
		mockRunnable1.On("Stop").Maybe()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner and save it to the captured variable
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)
		capturedRunner = runner

		// Run - should complete with error when the runnable "fails"
		err = runner.Run(context.Background())

		// Verify error and state
		assert.Error(t, err)
		assert.ErrorContains(t, err, "runnable error")
		assert.Equal(t, finitestate.StatusError, runner.GetState())

		mockRunnable1.AssertExpectations(t)
	})

	t.Run("runnable fails during startup", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()
		mockRunnable1.On("Stop").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Run", mock.Anything).Return(errors.New("failed to start"))
		mockRunnable2.On("Stop").Maybe()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Run
		err = runner.Run(context.Background())

		// Verify error and state
		assert.Error(t, err)
		assert.ErrorContains(t, err, "child runnable failed")
		assert.Equal(t, finitestate.StatusError, runner.GetState())

		// Verify mock expectations
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("missing config", func(t *testing.T) {
		t.Parallel()

		// Create config callback that initially returns a config but then nil
		callCount := 0
		configCallback := func() (*Config[*mocks.Runnable], error) {
			callCount++
			if callCount == 1 {
				entries := []RunnableEntry[*mocks.Runnable]{}
				return NewConfig("test", entries)
			}
			return nil, nil
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Force refresh of config during boot by clearing stored config
		runner.currentConfig.Store(nil)

		// Run should fail due to missing config
		err = runner.Run(context.Background())
		assert.Error(t, err)
		assert.ErrorContains(t, err, "no runnables to manage")
	})

	t.Run("no runnables", func(t *testing.T) {
		t.Parallel()

		// Create config callback with empty entries
		configCallback := func() (*Config[*mocks.Runnable], error) {
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Run should fail due to no runnables
		err = runner.Run(context.Background())
		assert.Error(t, err)
		assert.ErrorContains(t, err, "no runnables to manage")
	})
}

func TestCompositeRunner_Stop(t *testing.T) {
	t.Parallel()

	// Create reusable mock setup function
	setupMocksAndConfig := func() (entries []RunnableEntry[*mocks.Runnable], configFunc func() (*Config[*mocks.Runnable], error)) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()
		mockRunnable1.On("Stop").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()
		mockRunnable2.On("Stop").Maybe()

		// Create entries
		entries = []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create config callback
		configFunc = func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		return entries, configFunc
	}

	t.Run("stop from running state", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnables and config
		_, configCallback := setupMocksAndConfig()

		// Create runner and manually set state to Running
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// Call Stop - just testing the state transition
		runner.Stop()

		// Verify state transition
		assert.Equal(t, finitestate.StatusStopping, runner.GetState())
	})

	t.Run("stop from non-running state", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnables and config
		_, configCallback := setupMocksAndConfig()

		// Create runner and manually set state to Stopped
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusStopped)
		require.NoError(t, err)

		// Call Stop
		runner.Stop()

		// Verify state did not change
		assert.Equal(t, finitestate.StatusStopped, runner.GetState())
	})
}

// TestGetState_PassThrough tests the GetState method passes through to the underlying FSM
func TestGetState_PassThrough(t *testing.T) {
	t.Parallel()

	// Create a mock FSM
	mockFSM := new(MockStateMachine)
	mockFSM.On("GetState").Return(finitestate.StatusRunning)

	// Create a runner
	runner, err := NewRunner(
		WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}),
	)
	require.NoError(t, err)

	// Replace the FSM with our mock
	runner.fsm = mockFSM

	// Call GetState
	state := runner.GetState()

	// Verify it returns the state from the FSM
	assert.Equal(t, finitestate.StatusRunning, state)
	mockFSM.AssertExpectations(t)
}

// TestGetStateChan_PassThrough tests the GetStateChan method passes through to the underlying FSM
func TestGetStateChan_PassThrough(t *testing.T) {
	t.Parallel()

	// Create a test channel to return - must be a receive-only channel
	ch := make(chan string, 1)
	var stateCh <-chan string = ch

	// Create a mock FSM
	mockFSM := new(MockStateMachine)
	mockFSM.On("GetStateChan", mock.Anything).Return(stateCh)

	// Create a runner
	runner, err := NewRunner(
		WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}),
	)
	require.NoError(t, err)

	// Replace the FSM with our mock
	runner.fsm = mockFSM

	// Call GetStateChan
	ctx := context.Background()
	resultCh := runner.GetStateChan(ctx)

	// Verify it returns the channel from the FSM
	assert.Equal(t, stateCh, resultCh)
	mockFSM.AssertExpectations(t)
}

// TestGetChildStates_WithStateables tests the GetChildStates method with stateable runnables
func TestGetChildStates_WithStateables(t *testing.T) {
	t.Parallel()

	// Create a mock stateable runnable
	mockStateable := mocks.NewMockRunnableWithStatable()
	mockStateable.On("String").Return("stateable").Maybe()
	mockStateable.On("GetState").Return("running")
	mockStateable.On("Stop").Maybe()
	mockStateable.On("Run", mock.Anything).Return(nil).Maybe()

	// Create a normal mock runnable (not stateable)
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("String").Return("runnable").Maybe()
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()

	// Create entries with mockStateable
	type mixedRunnable interface {
		supervisor.Runnable
	}

	entries := []RunnableEntry[mixedRunnable]{
		{Runnable: mockStateable, Config: nil},
		{Runnable: mockRunnable, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[mixedRunnable], error) {
		return NewConfig("test", entries)
	}

	// Create runner
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Call GetChildStates
	states := runner.GetChildStates()

	// Verify states
	expected := map[string]string{
		"stateable": "running",
		"runnable":  "unknown",
	}
	assert.Equal(t, expected, states)

	mockStateable.AssertExpectations(t)
}

// TestCompositeRunner_GetStringWithConfig tests the String method with a config
func TestCompositeRunner_GetStringWithConfig(t *testing.T) {
	t.Parallel()

	// Create mock runnables
	mockRunnable1 := mocks.NewMockRunnable()
	mockRunnable1.On("String").Return("runnable1").Maybe()

	mockRunnable2 := mocks.NewMockRunnable()
	mockRunnable2.On("String").Return("runnable2").Maybe()

	// Create entries
	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: nil},
		{Runnable: mockRunnable2, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test-runner", entries)
	}

	// Create runner
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Get string representation
	str := runner.String()

	// Verify it includes the config name and entry count
	assert.Contains(t, str, "test-runner")
	assert.Contains(t, str, "entries: 2")
}

// TestCompositeRunner_GetStringWithNilConfig tests the String method with a nil config
func TestCompositeRunner_GetStringWithNilConfig(t *testing.T) {
	t.Parallel()

	// Create config callback that returns nil
	configCallback := func() (*Config[*mocks.Runnable], error) {
		return nil, nil
	}

	// Create runner
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Clear any cached config
	runner.currentConfig.Store(nil)

	// Get string representation
	str := runner.String()

	// Verify it shows nil config
	assert.Equal(t, "CompositeRunner<nil>", str)
}
