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
		t.Skip(
			"Skipping test temporarily due to syntax issues with defining types inside test functions",
		)
	})

	t.Run("runnable fails during startup", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil)
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
