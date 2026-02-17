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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := NewRunner(tt.callback, tt.opts...)
			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, runner)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, runner)
			}
		})
	}
}

func TestCompositeRunner_String(t *testing.T) {
	t.Parallel()
	mockRunnable1 := mocks.NewMockRunnable()
	mockRunnable2 := mocks.NewMockRunnable()

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
		mockRunnable1.On("String").Return("runnable1")
		mockRunnable1.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // Block until cancelled like a real service
		})
		mockRunnable1.On("Stop").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2")
		mockRunnable2.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // Block until cancelled like a real service
		})
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

		require.NoError(t, runErr, "Run should complete without error")
		assert.Equal(t, finitestate.StatusStopped, runner.GetState())
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("runnable fails during execution", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1")

		var capturedRunner *Runner[*mocks.Runnable]
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			// Create a goroutine that will send an error to the serverErrors channel
			go func() {
				// Use a timer channel instead of sleep for better testability
				timer := time.NewTimer(50 * time.Millisecond)
				<-timer.C
				capturedRunner.serverErrors <- errors.New("runnable error")
			}()

			// Wait until context is cancelled
			<-args.Get(0).(context.Context).Done()
		}).Return(nil)
		mockRunnable1.On("Stop").Once()

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
		require.Error(t, err)
		require.ErrorContains(t, err, "runnable error")
		assert.Equal(t, finitestate.StatusError, runner.GetState())

		mockRunnable1.AssertExpectations(t)
	})

	t.Run("runnable fails during startup", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1")
		mockRunnable.On("Run", mock.Anything).Return(errors.New("failed to start"))
		mockRunnable.On("Stop").Once()

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}

		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		err = runner.Run(context.Background())

		require.Error(t, err)
		require.ErrorContains(t, err, "child runnable failed")
		assert.Equal(t, finitestate.StatusError, runner.GetState())

		mockRunnable.AssertExpectations(t)
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
		require.NoError(t, runErr)
	})

	t.Run("empty entries with later reload", func(t *testing.T) {
		t.Parallel()

		// Channel to signal when Run() is called
		runStarted := make(chan struct{})

		// Setup mock runnable for reload
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable1")
		mockRunnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			close(runStarted) // Signal that Run was called
			ctx := args.Get(0).(context.Context)
			<-ctx.Done() // Block until cancelled like a real service
		})
		mockRunnable.On("Stop").Once()

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
		runner.Reload(t.Context())

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

		// Wait for the new runnable to actually start
		select {
		case <-runStarted:
			// Run() was called, safe to proceed
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for runnable to start after reload")
		}

		// Cancel context to stop runner
		cancel()

		// Verify clean shutdown
		var runErr error
		select {
		case runErr = <-errCh:
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timeout waiting for Run to complete")
		}
		require.NoError(t, runErr)

		// Verify expectations
		mockRunnable.AssertExpectations(t)
	})
}

func TestCompositeRunner_Stop(t *testing.T) {
	t.Parallel()

	t.Run("stop blocks until run completes", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			mock1 := mocks.NewMockRunnable()
			mock1.On("String").Return("r1")
			mock1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(nil)
			mock1.On("Stop").Once()

			entries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mock1},
			}

			configCallback := func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", entries)
			}

			runner, err := NewRunner(configCallback)
			require.NoError(t, err)

			errCh := make(chan error, 1)
			runReturned := atomic.Bool{}
			go func() {
				errCh <- runner.Run(t.Context())
				runReturned.Store(true)
			}()

			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
			assert.Equal(t, finitestate.StatusRunning, runner.GetState())

			runner.Stop()
			synctest.Wait()

			assert.True(t, runReturned.Load(), "Run should have returned before Stop unblocked")
			assert.Equal(t, finitestate.StatusStopped, runner.GetState())
			require.NoError(t, <-errCh)

			mock1.AssertExpectations(t)
		})
	})

	t.Run("stop before run", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			mock1 := mocks.NewMockRunnable()
			mock1.On("String").Return("r1")
			mock1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(nil)
			mock1.On("Stop").Once()

			entries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mock1},
			}

			configCallback := func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", entries)
			}

			runner, err := NewRunner(configCallback)
			require.NoError(t, err)

			stopReturned := atomic.Bool{}
			go func() {
				runner.Stop()
				stopReturned.Store(true)
			}()

			time.Sleep(10 * time.Millisecond)
			synctest.Wait()

			assert.False(t, stopReturned.Load(), "Stop should block until Run starts and completes")

			errCh := make(chan error, 1)
			go func() {
				errCh <- runner.Run(t.Context())
			}()

			time.Sleep(10 * time.Millisecond)
			synctest.Wait()

			assert.True(t, stopReturned.Load(), "Stop should unblock after Run completes")
			require.NoError(t, <-errCh)

			mock1.AssertExpectations(t)
		})
	})

	t.Run("multiple stop calls", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			mock1 := mocks.NewMockRunnable()
			mock1.DelayStop = 0
			mock1.On("String").Return("r1")
			mock1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(nil)
			mock1.On("Stop").Once()

			entries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mock1},
			}

			configCallback := func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", entries)
			}

			runner, err := NewRunner(configCallback)
			require.NoError(t, err)

			errCh := make(chan error, 1)
			go func() {
				errCh <- runner.Run(t.Context())
			}()

			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
			assert.Equal(t, finitestate.StatusRunning, runner.GetState())

			var wg sync.WaitGroup
			for range 5 {
				wg.Go(func() {
					runner.Stop()
				})
			}
			wg.Wait()

			assert.Equal(t, finitestate.StatusStopped, runner.GetState())
			require.NoError(t, <-errCh)
			mock1.AssertExpectations(t)
		})
	})

	t.Run("context cancel and stop race", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			mock1 := mocks.NewMockRunnable()
			mock1.On("String").Return("r1")
			mock1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(nil)
			mock1.On("Stop").Once()

			entries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mock1},
			}

			configCallback := func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", entries)
			}

			runner, err := NewRunner(configCallback)
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(t.Context())

			errCh := make(chan error, 1)
			go func() {
				errCh <- runner.Run(ctx)
			}()

			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
			assert.Equal(t, finitestate.StatusRunning, runner.GetState())

			// Fire both signals concurrently — Run's select may see either one
			go cancel()
			go runner.Stop()

			time.Sleep(10 * time.Millisecond)
			synctest.Wait()

			assert.Equal(t, finitestate.StatusStopped, runner.GetState())
			require.NoError(t, <-errCh)
			mock1.AssertExpectations(t)
		})
	})

	t.Run("stop blocks through slow child shutdown", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			mock1 := mocks.NewMockRunnable()
			mock1.DelayStop = 100 * time.Millisecond
			mock1.On("String").Return("r1")
			mock1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
				<-args.Get(0).(context.Context).Done()
			}).Return(nil)
			mock1.On("Stop").Once()

			entries := []RunnableEntry[*mocks.Runnable]{
				{Runnable: mock1},
			}

			configCallback := func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", entries)
			}

			runner, err := NewRunner(configCallback)
			require.NoError(t, err)

			errCh := make(chan error, 1)
			runReturned := atomic.Bool{}
			go func() {
				errCh <- runner.Run(t.Context())
				runReturned.Store(true)
			}()

			time.Sleep(10 * time.Millisecond)
			synctest.Wait()
			assert.Equal(t, finitestate.StatusRunning, runner.GetState())

			runner.Stop()
			synctest.Wait()

			assert.True(t, runReturned.Load(), "Run should have returned after slow child Stop completed")
			assert.Equal(t, finitestate.StatusStopped, runner.GetState())
			require.NoError(t, <-errCh)
			mock1.AssertExpectations(t)
		})
	})
}

func TestCompositeRunner_StopCancelsChildContexts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		childDone := make(chan struct{}, 2)

		mock1 := mocks.NewMockRunnable()
		mock1.On("String").Return("r1")
		mock1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
			childDone <- struct{}{}
		}).Return(nil)
		mock1.On("Stop").Once()

		mock2 := mocks.NewMockRunnable()
		mock2.On("String").Return("r2")
		mock2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
			childDone <- struct{}{}
		}).Return(nil)
		mock2.On("Stop").Once()

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mock1},
			{Runnable: mock2},
		}

		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		errCh := make(chan error, 1)
		go func() {
			errCh <- runner.Run(t.Context())
		}()

		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		assert.Equal(t, finitestate.StatusRunning, runner.GetState())

		runner.Stop()

		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		select {
		case err := <-errCh:
			require.NoError(t, err)
		default:
			t.Fatal("Run did not return after Stop — children may not derive from runCtx")
		}

		for range 2 {
			select {
			case <-childDone:
			default:
				t.Fatal("child goroutine did not exit after Stop")
			}
		}

		mock1.AssertExpectations(t)
		mock2.AssertExpectations(t)
	})
}

func TestCompositeRunner_StopDuringReload(t *testing.T) {
	t.Parallel()

	mock1 := mocks.NewMockRunnable()
	mock1.DelayReload = 50 * time.Millisecond
	mock1.On("String").Return("r1")
	mock1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
		<-args.Get(0).(context.Context).Done()
	}).Return(nil)
	mock1.On("Stop").Maybe()
	mock1.On("Reload", mock.Anything).Maybe()

	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mock1},
	}

	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", entries)
	}

	runner, err := NewRunner(configCallback)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Run(t.Context())
	}()

	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 1*time.Second, 5*time.Millisecond)

	// Start reload in background (holds reloadMu for DelayReload duration)
	go runner.Reload(t.Context())
	require.Eventually(t, func() bool {
		return runner.GetState() == finitestate.StatusReloading
	}, 1*time.Second, 1*time.Millisecond)

	// Stop while reload is in progress — must not deadlock
	stopReturned := atomic.Bool{}
	go func() {
		runner.Stop()
		stopReturned.Store(true)
	}()

	require.Eventually(t, func() bool {
		return stopReturned.Load()
	}, 5*time.Second, 10*time.Millisecond, "Stop must not deadlock when called during reload")

	// Run should also return
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop during reload")
	}
}

func TestCompositeRunner_ErrorPathStopsSiblings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		survivor := mocks.NewMockRunnable()
		survivor.On("String").Return("survivor")
		survivor.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			<-args.Get(0).(context.Context).Done()
		}).Return(nil)
		survivor.On("Stop").Once()

		failer := mocks.NewMockRunnable()
		failer.On("String").Return("failer")
		failer.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			time.Sleep(50 * time.Millisecond)
		}).Return(errors.New("boom"))
		failer.On("Stop").Once()

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: survivor},
			{Runnable: failer},
		}

		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		errCh := make(chan error, 1)
		go func() {
			errCh <- runner.Run(t.Context())
		}()

		time.Sleep(60 * time.Millisecond)
		synctest.Wait()

		select {
		case err := <-errCh:
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRunnableFailed)
			require.ErrorContains(t, err, "boom")
		default:
			t.Fatal("Run should have returned error from failing child")
		}

		survivor.AssertExpectations(t)
		failer.AssertExpectations(t)
	})
}

func TestCompositeRunner_MultipleChildFailures(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		failErr := errors.New("child failed")
		started := make(chan struct{})

		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("failer1")
		mockRunnable1.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			started <- struct{}{}
			time.Sleep(20 * time.Millisecond)
		}).Return(failErr)
		mockRunnable1.On("Stop").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("failer2")
		mockRunnable2.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			started <- struct{}{}
			time.Sleep(20 * time.Millisecond)
		}).Return(failErr)
		mockRunnable2.On("Stop").Once()

		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("failer3")
		mockRunnable3.On("Run", mock.Anything).Run(func(args mock.Arguments) {
			started <- struct{}{}
			time.Sleep(20 * time.Millisecond)
		}).Return(failErr)
		mockRunnable3.On("Stop").Once()

		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1},
			{Runnable: mockRunnable2},
			{Runnable: mockRunnable3},
		}

		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		runner, err := NewRunner(configCallback)
		require.NoError(t, err)

		assert.Equal(t, 1, cap(runner.serverErrors), "initial capacity should be 1")

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(t.Context())
		}()

		// Advance virtual clock past the mock's default 1ms Run delay
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		for range 3 {
			<-started
		}

		assert.Equal(t, 3, cap(runner.serverErrors), "capacity should match entry count after boot")

		// Advance virtual clock past 20ms sleep in callbacks so children return errors
		time.Sleep(30 * time.Millisecond)
		synctest.Wait()

		select {
		case err := <-runErr:
			require.Error(t, err)
			require.ErrorIs(t, err, ErrRunnableFailed)
		default:
			t.Fatal("runner should return an error from failing children")
		}

		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
		mockRunnable3.AssertExpectations(t)
	})
}
