package composite

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func setupTestLogger() slog.Handler {
	return slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
}

func TestCompositeInterfaceImplementation(t *testing.T) {
	t.Parallel()

	var (
		_ supervisor.Runnable   = (*Runner[supervisor.Runnable])(nil)
		_ supervisor.Reloadable = (*Runner[supervisor.Runnable])(nil)
		_ supervisor.Stateable  = (*Runner[supervisor.Runnable])(nil)
	)
}

func TestNewRunner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		callback    ConfigCallback[*mocks.Runnable]
		opts        []Option[*mocks.Runnable]
		expectError bool
	}{
		{
			name: "valid with required options",
			callback: func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
			},
			opts:        nil,
			expectError: false,
		},
		{
			name: "config callback returns error",
			callback: func() (*Config[*mocks.Runnable], error) {
				return nil, errors.New("config error")
			},
			opts:        nil,
			expectError: false,
		},
		{
			name: "config callback returns nil",
			callback: func() (*Config[*mocks.Runnable], error) {
				return nil, nil
			},
			opts:        nil,
			expectError: false,
		},
		{
			name: "with custom logger",
			callback: func() (*Config[*mocks.Runnable], error) {
				entries := []RunnableEntry[*mocks.Runnable]{}
				return NewConfig("test", entries)
			},
			opts: []Option[*mocks.Runnable]{
				WithLogHandler[*mocks.Runnable](setupTestLogger()),
			},
			expectError: false,
		},
		{
			name: "with custom context",
			callback: func() (*Config[*mocks.Runnable], error) {
				entries := []RunnableEntry[*mocks.Runnable]{}
				return NewConfig("test", entries)
			},
			opts: []Option[*mocks.Runnable]{
				WithContext[*mocks.Runnable](context.Background()),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := NewRunner(tt.callback, tt.opts...)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, runner)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, runner)
			}
		})
	}
}

func TestCompositeRunner_String(t *testing.T) {
	t.Parallel()
	mockRunnable1 := mocks.NewMockRunnable()
	mockRunnable1.On("String").Return("runnable1").Maybe()
	mockRunnable1.On("Stop").Maybe()
	mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()

	mockRunnable2 := mocks.NewMockRunnable()
	mockRunnable2.On("String").Return("runnable2").Maybe()
	mockRunnable2.On("Stop").Maybe()
	mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()

	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: nil},
		{Runnable: mockRunnable2, Config: nil},
	}

	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test-runner", entries)
	}

	runner, err := NewRunner(configCallback)
	require.NoError(t, err)

	str := runner.String()
	assert.Contains(t, str, "CompositeRunner")
	assert.Contains(t, str, "test-runner")
	assert.Contains(t, str, "2")
}

func TestCompositeRunner_Run(t *testing.T) {
	t.Parallel()

	t.Run("successful run and graceful shutdown", func(t *testing.T) {
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
		runner, err := NewRunner(configCallback)
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

		var runErr error
		assert.Eventually(t, func() bool {
			select {
			case runErr = <-errCh:
				return true
			default:
				return false
			}
		}, 500*time.Millisecond, 10*time.Millisecond, "Run should complete")

		assert.NoError(t, runErr, "Run should complete without error")
		assert.Equal(t, finitestate.StatusStopped, runner.GetState())
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("runnable fails during execution", func(t *testing.T) {
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
		runner, err := NewRunner(configCallback)
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
		runner, err := NewRunner(configCallback)
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
		// Do not run in parallel to avoid interference
		// Config callback always returns nil, triggering configuration unavailable error
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, nil
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Run and expect an error immediately
		err = runner.Run(context.Background())
		require.Error(t, err)
		assert.ErrorContains(t, err, "configuration is unavailable")
	})

	t.Run("no runnables", func(t *testing.T) {
		t.Parallel()

		// Create config callback with empty entries
		configCallback := func() (*Config[*mocks.Runnable], error) {
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Run in a goroutine that we can cancel
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)

		go func() {
			errCh <- runner.Run(ctx)
		}()

		// Wait for states to transition to Running
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 500*time.Millisecond, 10*time.Millisecond, "Runner should transition to Running state")

		// Verify runner is running with no entries
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())

		cfg := runner.getConfig()
		require.NotNil(t, cfg)
		assert.Empty(t, cfg.Entries, "Config should have empty entries")

		// Graceful shutdown
		cancel()

		// Verify clean shutdown
		var runErr error
		select {
		case runErr = <-errCh:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timeout waiting for Run to complete")
		}
		assert.NoError(t, runErr)
	})

	t.Run("empty entries with later reload", func(t *testing.T) {
		t.Parallel()

		// Setup mock runnable for reload
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1").Maybe()
		mockRunnable.On("Run", mock.Anything).Return(nil)
		mockRunnable.On("Stop").Maybe()

		// Initially empty, later populated
		var useUpdatedEntries atomic.Bool

		// Create config callback that initially returns empty entries,
		// but returns entries with a runnable after useUpdatedEntries is set
		configCallback := func() (*Config[*mocks.Runnable], error) {
			if useUpdatedEntries.Load() {
				entries := []RunnableEntry[*mocks.Runnable]{
					{Runnable: mockRunnable, Config: nil},
				}
				return NewConfig("test", entries)
			}
			// Initial empty config
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		// Run in a goroutine
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			errCh <- runner.Run(ctx)
		}()

		// Wait for states to transition to Running (even with no entries)
		require.Eventually(t, func() bool {
			return runner.GetState() == finitestate.StatusRunning
		}, 500*time.Millisecond, 10*time.Millisecond, "Runner should transition to Running state")

		// Verify initial empty state
		initialCfg := runner.getConfig() // Store the initial config pointer
		require.NotNil(t, initialCfg)
		assert.Empty(t, initialCfg.Entries, "Initial config should have empty entries")

		// Now update the entries and reload
		useUpdatedEntries.Store(true)
		runner.Reload()

		// Wait for reload to complete by checking if the config pointer has changed
		var updatedCfg *Config[*mocks.Runnable]
		assert.Eventually(t, func() bool {
			updatedCfg = runner.getConfig()
			// Condition is met when the config pointer is different from the initial one
			return updatedCfg != initialCfg
		}, 200*time.Millisecond, 10*time.Millisecond, "Config should be updated after reload")

		// Verify the config was updated with new entries
		require.NotNil(t, updatedCfg)
		require.Len(t, updatedCfg.Entries, 1, "Config should now have 1 entry after reload")
		assert.Equal(
			t,
			mockRunnable,
			updatedCfg.Entries[0].Runnable,
			"Config should contain the mock runnable",
		)

		// Cancel context to stop runner
		cancel()

		// Verify clean shutdown
		var runErr error
		select {
		case runErr = <-errCh:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timeout waiting for Run to complete")
		}
		assert.NoError(t, runErr)

		// Verify expectations
		mockRunnable.AssertExpectations(t)
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
		runner, err := NewRunner(configCallback)
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
		runner, err := NewRunner(configCallback)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusStopped)
		require.NoError(t, err)

		// Call Stop
		runner.Stop()

		// Verify state did not change
		assert.Equal(t, finitestate.StatusStopped, runner.GetState())
	})
}
