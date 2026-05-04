package supervisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Tests for supervisor options and initialization

// TestPIDZero_NewPIDZero tests creating a new PIDZero instance.
func TestPIDZero_NewPIDZero(t *testing.T) {
	// Create a mock runnable for testing
	mockRunnable := mocks.NewMockRunnable()
	stateChan := make(chan string)
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	ctx := context.Background()
	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	assert.NotNil(t, pid0)
	assert.Len(t, pid0.runnables, 1)

	assert.Equal(t, "Supervisor<runnables: 1>", pid0.String())
}

// TestPIDZero_WithLogHandler tests the WithLogHandler option.
func TestPIDZero_WithLogHandler(t *testing.T) {
	t.Parallel()

	// Create a custom log handler
	customHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})

	// Create a PIDZero with the custom log handler
	mockRunnable := mocks.NewMockRunnable()
	stateChan := make(chan string)
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()
	pid0, err := New(WithRunnables(mockRunnable), WithLogHandler(customHandler))
	require.NoError(t, err)

	// Verify that the logger was set correctly
	// We can't directly compare the loggers, but we can verify it's not nil
	assert.NotNil(t, pid0.logger)

	// Test with nil handler (should use default)
	mockRunnable2 := mocks.NewMockRunnable()
	stateChan2 := make(chan string)
	mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()
	mockRunnable2.On("Stop").Maybe()
	mockRunnable2.On("GetState").Return("running").Maybe()
	mockRunnable2.On("GetStateChan", mock.Anything).Return(stateChan2).Maybe()
	defaultPid0, err := New(WithRunnables(mockRunnable2), WithLogHandler(nil))
	require.NoError(t, err)
	assert.NotNil(t, defaultPid0.logger)
}

// TestPIDZero_WithSignals tests the WithSignals option.
func TestPIDZero_WithSignals(t *testing.T) {
	t.Parallel()

	// Create custom signals
	customSignals := []os.Signal{syscall.SIGUSR1, syscall.SIGUSR2}

	// Create a PIDZero with custom signals
	mockRunnable := mocks.NewMockRunnable()
	stateChan := make(chan string)
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()
	pid0, err := New(WithRunnables(mockRunnable), WithSignals(customSignals...))
	require.NoError(t, err)

	// Verify that the signals were set correctly
	assert.Equal(t, customSignals, pid0.subscribeSignals)
	assert.Len(t, pid0.subscribeSignals, 2)
	assert.Contains(t, pid0.subscribeSignals, syscall.SIGUSR1)
	assert.Contains(t, pid0.subscribeSignals, syscall.SIGUSR2)

	// Verify the default signals are used when not specified
	mockRunnable2 := mocks.NewMockRunnable()
	stateChan2 := make(chan string)
	mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()
	mockRunnable2.On("Stop").Maybe()
	mockRunnable2.On("GetState").Return("running").Maybe()
	mockRunnable2.On("GetStateChan", mock.Anything).Return(stateChan2).Maybe()
	defaultPid0, err := New(WithRunnables(mockRunnable2))
	require.NoError(t, err)
	assert.Len(t, defaultPid0.subscribeSignals, 3)
	assert.Contains(t, defaultPid0.subscribeSignals, syscall.SIGINT)
	assert.Contains(t, defaultPid0.subscribeSignals, syscall.SIGTERM)
	assert.Contains(t, defaultPid0.subscribeSignals, syscall.SIGHUP)
}

// TestPIDZero_OptionEdgeCases tests edge cases in option functions.
func TestPIDZero_OptionEdgeCases(t *testing.T) {
	t.Parallel()

	mockRunnable := mocks.NewMockRunnable()
	stateChan := make(chan string)
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	t.Run("zero timeout values", func(t *testing.T) {
		pid0, err := New(
			WithRunnables(mockRunnable),
			WithStartupTimeout(0),  // Ignored — startup needs a deadline
			WithStartupInitial(0),  // Ignored — initial wait must be > 0
			WithShutdownTimeout(0), // Accepted — disables the shutdown deadline
		)
		require.NoError(t, err)

		assert.Equal(t, DefaultStartupTimeout, pid0.startupTimeout)
		assert.Equal(t, DefaultStartupInitial, pid0.startupInitial)
		assert.Equal(t, time.Duration(0), pid0.shutdownTimeout,
			"WithShutdownTimeout(0) should disable the deadline (Shutdown waits indefinitely)")
	})

	t.Run("nil context is ignored", func(t *testing.T) {
		pid0, err := New(
			WithRunnables(mockRunnable),
		)
		require.NotNil(t, pid0)
		require.NoError(t, err)
		assert.NotNil(t, pid0.ctx)
	})

	t.Run("empty runnables slice returns an error", func(t *testing.T) {
		_, err := New(
			WithRunnables(), // Empty slice
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no runnables provided")
	})

	t.Run("no runnables slice returns an error", func(t *testing.T) {
		_, err := New()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no runnables provided")
	})
}

func TestBlockUntilRunnableReady(t *testing.T) {
	t.Parallel()

	t.Run("immediately ready", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnableWithStateable()
		mockRunnable.On("String").Return("ready-runnable").Maybe()
		mockRunnable.On("IsRunning").Return(true).Once()

		sv, err := New(
			WithRunnables(mockRunnable),
			WithStartupTimeout(100*time.Millisecond),
		)
		require.NoError(t, err)

		result := assert.Eventually(t, func() bool {
			err := sv.blockUntilRunnableReady(mockRunnable)
			return err == nil
		}, 200*time.Millisecond, 10*time.Millisecond, "Should return quickly without error")

		assert.True(t, result, "blockUntilRunnableReady should complete successfully")
		mockRunnable.AssertExpectations(t)
	})

	t.Run("becomes ready after delay", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnableWithStateable()
		mockRunnable.On("String").Return("delayed-runnable").Maybe()
		mockRunnable.On("IsRunning").Return(false).Once()
		mockRunnable.On("IsRunning").Return(false).Once()
		mockRunnable.On("IsRunning").Return(true).Once()

		sv, err := New(
			WithRunnables(mockRunnable),
			WithStartupTimeout(500*time.Millisecond),
			WithStartupInitial(10*time.Millisecond),
		)
		require.NoError(t, err)

		result := assert.Eventually(t, func() bool {
			err := sv.blockUntilRunnableReady(mockRunnable)
			return err == nil
		}, 300*time.Millisecond, 10*time.Millisecond, "Should succeed after delay")

		assert.True(t, result, "blockUntilRunnableReady should eventually succeed")
		mockRunnable.AssertExpectations(t)
	})

	t.Run("timeout", func(t *testing.T) {
		// Setup mock that never reports as running
		mockRunnable := mocks.NewMockRunnableWithStateable()
		mockRunnable.On("String").Return("stuck-runnable").Maybe()
		mockRunnable.On("IsRunning").Return(false).Maybe() // Always returns false

		// Create supervisor with a very short timeout
		sv, err := New(
			WithRunnables(mockRunnable),
			WithStartupTimeout(50*time.Millisecond),
			WithStartupInitial(10*time.Millisecond),
		)
		require.NoError(t, err)

		// Call blockUntilRunnableReady - should timeout
		var resultErr error
		assert.Eventually(t, func() bool {
			resultErr = sv.blockUntilRunnableReady(mockRunnable)
			return resultErr != nil
		}, 200*time.Millisecond, 10*time.Millisecond, "Should return error when timeout occurs")

		require.Error(t, resultErr)
		assert.Contains(t, resultErr.Error(), "timeout waiting for runnable to start")
		mockRunnable.AssertExpectations(t)
	})

	t.Run("parent context canceled", func(t *testing.T) {
		// Setup mock that never reports as running
		mockRunnable := mocks.NewMockRunnableWithStateable()
		mockRunnable.On("String").Return("canceled-runnable").Maybe()
		mockRunnable.On("IsRunning").Return(false).Maybe() // Always returns false

		// Create supervisor with a context
		ctx, cancel := context.WithCancel(context.Background())
		sv, err := New(
			WithContext(ctx),
			WithRunnables(mockRunnable),
			WithStartupTimeout(500*time.Millisecond),
			WithStartupInitial(10*time.Millisecond),
		)
		require.NoError(t, err)

		// Cancel the context right away. blockUntilRunnableReady must surface
		// this as ctx.Err() so the Run loop bails instead of advancing to
		// the next runnable.
		cancel()

		err = sv.blockUntilRunnableReady(mockRunnable)
		require.ErrorIs(t, err, context.Canceled)
		require.NotContains(t, err.Error(), "timeout",
			"ctx-cancel must not be wrapped as a timeout error")
		mockRunnable.AssertExpectations(t)
	})

	// startupCtx is derived from p.ctx, so when p.ctx is cancelled both
	// startupCtx.Done() and p.ctx.Done() are immediately ready and select
	// picks randomly. Repeat enough that without the precedence fix at
	// least one iteration takes the startupCtx branch and returns a
	// misleading "timeout" wrap.
	t.Run("cancel beats timeout when both fire", func(t *testing.T) {
		const iterations = 20
		for i := range iterations {
			mockRunnable := mocks.NewMockRunnableWithStateable()
			mockRunnable.On("String").Return("racy-runnable").Maybe()
			mockRunnable.On("IsRunning").Return(false).Maybe()

			ctx, cancel := context.WithCancel(context.Background())
			sv, err := New(
				WithContext(ctx),
				WithRunnables(mockRunnable),
				WithStartupTimeout(time.Second),
				WithStartupInitial(time.Millisecond),
			)
			require.NoError(t, err)

			cancel()

			err = sv.blockUntilRunnableReady(mockRunnable)
			require.ErrorIsf(t, err, context.Canceled, "iteration %d", i)
			require.NotContainsf(t, err.Error(), "timeout",
				"iteration %d: cancellation must take precedence over the startupCtx timeout wrap", i)
		}
	})

	t.Run("error from runnable", func(t *testing.T) {
		// Setup mock that never reports as running
		mockRunnable := mocks.NewMockRunnableWithStateable()
		mockRunnable.On("String").Return("error-runnable").Maybe()
		mockRunnable.On("IsRunning").Return(false).Maybe() // Always returns false

		sv, err := New(
			WithRunnables(mockRunnable),
			WithStartupTimeout(500*time.Millisecond),
			WithStartupInitial(10*time.Millisecond),
		)
		require.NoError(t, err)

		// Send an error through the error channel (simulating a runnable failure)
		expectedErr := errors.New("runnable startup error")
		go func() {
			assert.Eventually(t, func() bool {
				sv.errorChan <- expectedErr
				return true
			}, 50*time.Millisecond, 1*time.Millisecond, "Should send error to channel")
		}()

		// Call blockUntilRunnableReady - should return the error
		var resultErr error
		assert.Eventually(t, func() bool {
			resultErr = sv.blockUntilRunnableReady(mockRunnable)
			return resultErr != nil
		}, 200*time.Millisecond, 10*time.Millisecond, "Should return error from error channel")

		require.Error(t, resultErr)
		assert.Equal(t, expectedErr, resultErr, "Should return the exact error from error channel")

		// Verify the error channel doesn't have any remaining errors
		// since blockUntilRunnableReady should have consumed it
		select {
		case <-sv.errorChan:
			t.Fatal("Error channel should not have remaining errors")
		default:
			// This is expected - error was consumed by blockUntilRunnableReady
		}

		mockRunnable.AssertExpectations(t)
	})
}

// TestBlockUntilRunnableReady_TickerRetry tests the ticker retry logic with multiple checks.
func TestBlockUntilRunnableReady_TickerRetry(t *testing.T) {
	t.Parallel()

	// Create mock that becomes ready after multiple ticker retries
	mockRunnable := mocks.NewMockRunnableWithStateable()
	mockRunnable.On("String").Return("delayed-ready-runnable").Maybe()

	// First few calls return false, then true
	mockRunnable.On("IsRunning").Return(false).Times(4) // Initial check + 3 ticker retries
	mockRunnable.On("IsRunning").Return(true).Once()    // Finally becomes ready

	sv, err := New(
		WithRunnables(mockRunnable),
		WithStartupTimeout(500*time.Millisecond),
		WithStartupInitial(5*time.Millisecond), // Very short initial delay
	)
	require.NoError(t, err)

	// This should succeed after several ticker retries
	var resultErr error
	result := assert.Eventually(t, func() bool {
		resultErr = sv.blockUntilRunnableReady(mockRunnable)
		return resultErr == nil
	}, 300*time.Millisecond, 1*time.Millisecond, "Should succeed after ticker retries")

	assert.True(t, result, "blockUntilRunnableReady should eventually succeed")
	require.NoError(t, resultErr)
	mockRunnable.AssertExpectations(t)
}

// Tests for error handling and reaping processes

// TestPIDZero_Reap_ErrorFromRunnable tests that an error from a runnable initiates shutdown.
func TestPIDZero_Reap_ErrorFromRunnable(t *testing.T) {
	// Create a mock runnable
	mockRunnable := new(mocks.Runnable)
	mockRunnable.On("Run", mock.Anything).Return(errors.New("runnable error")).Once()
	mockRunnable.On("Stop").Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	// Create PIDZero with the mock runnable
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	// Ensure cleanup
	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Wait for Exec to finish due to runnable error
	select {
	case err := <-execDone:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "runnable error",
			"Error should contain the original runnable error")
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after runnable error")
	}

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_StartRunnable_ContextCancellationError tests filtering of context cancellation errors.
func TestPIDZero_StartRunnable_ContextCancellationError(t *testing.T) {
	t.Parallel()

	// Create a runnable that returns context.Canceled
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("Run", mock.Anything).Return(context.Canceled).Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("stopped").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	// startRunnable should filter out the context.Canceled error and return nil
	err = pid0.startRunnable(mockRunnable)
	require.NoError(t, err, "Context cancellation errors should be filtered out")

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_StartRunnable_DeadlineExceededError tests filtering of deadline exceeded errors.
func TestPIDZero_StartRunnable_DeadlineExceededError(t *testing.T) {
	t.Parallel()

	// Create a runnable that returns context.DeadlineExceeded
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("Run", mock.Anything).Return(context.DeadlineExceeded).Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("stopped").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	// startRunnable should filter out the deadline exceeded error and return nil
	err = pid0.startRunnable(mockRunnable)
	require.NoError(t, err, "Deadline exceeded errors should be filtered out")

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Reap_HandleSIGINT tests that receiving SIGINT initiates shutdown.
func TestPIDZero_Reap_HandleSIGINT(t *testing.T) {
	t.Parallel()
	mockRunnable := new(mocks.Runnable)
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Stop").Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Wait for services to start and send SIGINT signal
	var signalSent bool
	assert.Eventually(t, func() bool {
		if !signalSent {
			pid0.signalChan <- syscall.SIGINT
			signalSent = true
		}
		return true
	}, 200*time.Millisecond, 10*time.Millisecond, "Should send SIGINT signal")

	// Wait for Exec to finish due to SIGINT
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGINT")
	}

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Reap_HandleSIGTERM tests that receiving SIGTERM initiates shutdown.
func TestPIDZero_Reap_HandleSIGTERM(t *testing.T) {
	mockRunnable := new(mocks.Runnable)
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Stop").Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	ctx := t.Context()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Wait for services to start and send SIGTERM signal
	var signalSent bool
	assert.Eventually(t, func() bool {
		if !signalSent {
			pid0.signalChan <- syscall.SIGTERM
			signalSent = true
		}
		return true
	}, 200*time.Millisecond, 10*time.Millisecond, "Should send SIGTERM signal")

	// Wait for Exec to finish due to SIGTERM
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGTERM")
	}

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Reap_HandleSIGHUP tests that receiving SIGHUP triggers reload.
func TestPIDZero_Reap_HandleSIGHUP(t *testing.T) {
	t.Parallel()
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Reload", mock.Anything).Once()
	mockRunnable.On("Stop").Once()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	pid0.signalChan <- syscall.SIGHUP

	// Wait for reload to complete before sending shutdown signal
	require.Eventually(t, func() bool {
		return !mockRunnable.IsMethodCallable(t, "Reload", mock.Anything)
	}, 1*time.Second, 10*time.Millisecond, "Reload was not called after SIGHUP")

	pid0.signalChan <- syscall.SIGINT

	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGHUP and SIGINT")
	}

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Reap_MultipleSignals tests that SIGHUP triggers reload on multiple
// runnables, followed by SIGTERM triggering clean shutdown.
func TestPIDZero_Reap_MultipleSignals(t *testing.T) {
	t.Parallel()
	mockRunnable1 := mocks.NewMockRunnable()
	mockRunnable2 := mocks.NewMockRunnable()
	mockRunnable1.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable2.On("Run", mock.Anything).Return(nil).Once()

	mockRunnable1.On("Reload", mock.Anything).Once()
	mockRunnable2.On("Reload", mock.Anything).Once()

	mockRunnable1.On("Stop").Once()
	mockRunnable2.On("Stop").Once()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable1, mockRunnable2))
	require.NoError(t, err)

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	pid0.signalChan <- syscall.SIGHUP

	// Wait for reload to complete on both runnables before sending shutdown
	require.Eventually(t, func() bool {
		return !mockRunnable1.IsMethodCallable(t, "Reload", mock.Anything) &&
			!mockRunnable2.IsMethodCallable(t, "Reload", mock.Anything)
	}, 1*time.Second, 10*time.Millisecond, "Reload was not called on both runnables after SIGHUP")

	pid0.signalChan <- syscall.SIGTERM

	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after multiple signals")
	}

	mockRunnable1.AssertExpectations(t)
	mockRunnable2.AssertExpectations(t)
}

// TestPIDZero_Reap_NoRunnables tests behavior when there are no runnables.
func TestPIDZero_Reap_NoRunnables(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockService := mocks.NewMockRunnable()
	serviceStateChan := make(chan string)
	mockService.On("Run", mock.Anything).Return(nil).Maybe()
	mockService.On("Stop").Maybe()
	mockService.On("GetState").Return("running").Maybe()
	mockService.On("GetStateChan", mock.Anything).Return(serviceStateChan).Maybe()
	pid0, err := New(WithContext(ctx), WithRunnables(mockService))
	require.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Send SIGINT signal to shutdown
	pid0.signalChan <- syscall.SIGINT

	// Wait for Exec to finish
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time with no runnables")
	}
}

// TestPIDZero_Reap_ShutdownCalledOnce tests that Shutdown is only called once.
func TestPIDZero_Reap_ShutdownCalledOnce(t *testing.T) {
	t.Parallel()
	mockRunnable := new(mocks.Runnable)
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Stop").Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Wait for services to start and send two SIGINT signals
	var signalsSent int
	assert.Eventually(t, func() bool {
		if signalsSent < 2 {
			pid0.signalChan <- syscall.SIGINT
			signalsSent++
		}
		return signalsSent >= 2
	}, 200*time.Millisecond, 10*time.Millisecond, "Should send two SIGINT signals")

	// Wait for Exec to finish
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after multiple SIGINT")
	}

	// Ensure Shutdown was called only once
	mockRunnable.AssertNumberOfCalls(t, "Stop", 1)
}

// Tests for signal handling

// TestPIDZero_ShutdownIgnoresSIGHUP tests that SIGHUP signals are ignored during shutdown.
func TestPIDZero_ShutdownIgnoresSIGHUP(t *testing.T) {
	t.Parallel()
	// Create a mock runnable
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	// Expect Reload not to be called
	// Stop should be called once during shutdown
	mockRunnable.On("Stop").Once().After(500 * time.Millisecond)
	mockRunnable.On("Reload", mock.Anything).Panic("Reload should not be called")

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	// Create PIDZero with the mock runnable
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)

	// Ensure cleanup
	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Synchronize the two goroutines
	var wg sync.WaitGroup
	wg.Add(2)
	trigger1 := make(chan struct{})
	trigger2 := make(chan struct{})
	go func() {
		defer wg.Done()
		<-trigger1
		pid0.signalChan <- syscall.SIGTERM
	}()

	go func() {
		defer wg.Done()
		<-trigger2
		pid0.signalChan <- syscall.SIGHUP
	}()

	// Wait for services to start and trigger both goroutines
	assert.Eventually(t, func() bool {
		close(trigger1)
		time.Sleep(1 * time.Millisecond) // Brief delay between triggers
		close(trigger2)
		return true
	}, 200*time.Millisecond, 10*time.Millisecond, "Should trigger signal goroutines")

	// Wait for both goroutines to finish
	wg.Wait()

	// Wait for Exec to finish due to SIGTERM
	select {
	case err := <-execDone:
		require.NoError(t, err)
		t.Log("Exec finished without errors")
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGTERM and SIGHUP")
	}

	// Ensure Reload was not called
	mockRunnable.AssertNotCalled(t, "Reload")

	// Ensure Stop was called only once
	mockRunnable.AssertNumberOfCalls(t, "Stop", 1)
}

// TestPIDZero_CancelContextFromParent tests for a context cancel from parent.
func TestPIDZero_CancelContextFromParent(t *testing.T) {
	t.Parallel()
	// Create a mock runnable
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	// Expect Reload not to be called
	// Stop should be called once during shutdown
	mockRunnable.On("Stop").Once().After(500 * time.Millisecond)

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	// Create PIDZero with the mock runnable
	ctx, cancel := context.WithCancel(context.Background())
	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	require.NoError(t, err)
	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Cancel the context from parent
	cancel()

	// Wait for Exec to finish due to SIGTERM
	select {
	case err := <-execDone:
		require.NoError(t, err)
		t.Log("Exec finished without errors")
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGTERM and SIGHUP")
	}

	// Ensure Reload was not called
	mockRunnable.AssertNotCalled(t, "Reload")

	// Ensure Stop was called only once
	mockRunnable.AssertNumberOfCalls(t, "Stop", 1)
}

// TestPIDZero_Run_StartupCancelStopsLoop verifies that Run's startup loop bails
// when p.ctx is cancelled mid-startup, instead of advancing to spawn later
// runnables against an already-cancelled ctx.
//
// Regression: pre-fix, blockUntilRunnableReady returned nil on <-p.ctx.Done(),
// so the Run loop interpreted ctx-cancellation as "ready, proceed to next
// runnable" and kept spawning. With the fix, blockUntilRunnableReady returns
// p.ctx.Err() and the loop honors it.
//
// Cannot use synctest: pidZero.Run() calls signal.Notify, which is incompatible
// with synctest. Synchronization is via a sync.Once-guarded channel send from
// A's IsRunning callback — deterministic without timing assumptions.
func TestPIDZero_Run_StartupCancelStopsLoop(t *testing.T) {
	t.Parallel()

	// A: Stateable, IsRunning always returns false so blockUntilRunnableReady
	// stays in its polling loop. The first IsRunning call signals reachedCh,
	// which the test uses to know Run has entered blockUntilRunnableReady.
	reachedCh := make(chan struct{}, 1)
	var signalOnce sync.Once
	a := mocks.NewMockRunnableWithStateable()
	a.On("String").Return("a").Maybe()
	a.On("GetState").Return("not-running").Maybe()
	aStateChan := make(chan string)
	a.On("GetStateChan", mock.Anything).Return(aStateChan).Maybe()
	a.On("IsRunning").Return(false).Run(func(args mock.Arguments) {
		signalOnce.Do(func() { reachedCh <- struct{}{} })
	})
	a.On("Run", mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	})
	a.On("Stop").Once()

	// B: must NEVER have Run called. Registered .Maybe() so a regression
	// flips bRunCalled and the assertion at the end catches it (rather
	// than panicking inside B's goroutine via testify's missing-expectation
	// behavior).
	var bRunCalled atomic.Bool
	b := mocks.NewMockRunnable()
	b.On("String").Return("b").Maybe()
	b.On("Run", mock.Anything).Return(nil).Maybe().Run(func(args mock.Arguments) {
		bRunCalled.Store(true)
	})
	b.On("Stop").Maybe()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	pidZero := newTestPIDZero(t,
		WithContext(ctx),
		WithRunnables(a, b),
		WithStartupTimeout(2*time.Second),
		WithStartupInitial(10*time.Millisecond),
		WithShutdownTimeout(time.Second),
	)

	execDone := startPIDZeroRun(t, pidZero)

	// Wait until Run is inside blockUntilRunnableReady for A, then cancel.
	eventuallyClosed(t, reachedCh, "Run did not reach blockUntilRunnableReady for A")
	cancel()

	requirePIDZeroRunDone(t, execDone, 2*time.Second)

	require.False(t, bRunCalled.Load(),
		"B's Run must not be called after parent ctx cancelled mid-startup")
	require.Error(t, pidZero.ctx.Err(),
		"pidZero.ctx must be cancelled after the parent ctx is cancelled")
}

// TestPIDZero_Run_PreCancelledCtxStopsAtFirst verifies the top-of-iteration
// ctx check (i > 0): when Run is invoked with an already-cancelled p.ctx,
// the first runnable still spawns (preserving the long-standing contract
// that Run gives every registered runnable at least an attempt) but no
// later runnable is spawned. Without the iter>0 check, the supervisor
// could keep spawning subsequent runnables against an already-cancelled
// ctx between iterations.
func TestPIDZero_Run_PreCancelledCtxStopsAtFirst(t *testing.T) {
	t.Parallel()

	var bRunCalled atomic.Bool
	var cRunCalled atomic.Bool

	a := mocks.NewMockRunnable()
	a.On("String").Return("a").Maybe()
	a.On("Run", mock.Anything).Return(nil).Maybe()
	a.On("Stop").Maybe()

	b := mocks.NewMockRunnable()
	b.On("String").Return("b").Maybe()
	b.On("Run", mock.Anything).Return(nil).Maybe().Run(func(args mock.Arguments) {
		bRunCalled.Store(true)
	})
	b.On("Stop").Maybe()

	c := mocks.NewMockRunnable()
	c.On("String").Return("c").Maybe()
	c.On("Run", mock.Anything).Return(nil).Maybe().Run(func(args mock.Arguments) {
		cRunCalled.Store(true)
	})
	c.On("Stop").Maybe()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // cancel before Run is even called

	pidZero := newTestPIDZero(t,
		WithContext(ctx),
		WithRunnables(a, b, c),
		WithStartupTimeout(time.Second),
		WithShutdownTimeout(time.Second),
	)

	execDone := startPIDZeroRun(t, pidZero)
	requirePIDZeroRunDone(t, execDone, 2*time.Second)

	require.False(t, bRunCalled.Load(),
		"B's Run must not be called when parent ctx is pre-cancelled (iter > 0)")
	require.False(t, cRunCalled.Load(),
		"C's Run must not be called when parent ctx is pre-cancelled (iter > 0)")
}

// slowBootRunnable wraps MockRunnableWithStateable so the test can gate
// IsRunning() on a channel rather than testify's count-based mock returns.
// This holds the supervisor's startup loop in blockUntilRunnableReady until
// the gate is closed, giving the test a deterministic window to inject a
// signal mid-boot.
type slowBootRunnable struct {
	*mocks.MockRunnableWithStateable
	ready chan struct{}
}

func (s *slowBootRunnable) IsRunning() bool {
	select {
	case <-s.ready:
		return true
	default:
		return false
	}
}

// TestPIDZero_Run_SIGTERMDuringBoot covers Codex stability gap #8: SIGTERM
// arriving during the startup loop, before all Stateable runnables have
// transitioned to Running. signal.Notify's buffered channel queues the
// signal; the startup loop doesn't drain it (only reap does). Once boot
// completes the queued SIGTERM is processed and Shutdown runs cleanly.
//
// Cannot use synctest: this calls pidZero.Run(), which uses signal.Notify.
func TestPIDZero_Run_SIGTERMDuringBoot(t *testing.T) {
	t.Parallel()

	runStarted := make(chan struct{})
	ready := make(chan struct{})

	base := mocks.NewMockRunnableWithStateable()
	base.On("String").Return("slowBootRunnable").Maybe()
	base.On("Run", mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		close(runStarted)
		<-ctx.Done()
	})
	base.On("GetState").Return("Booting").Maybe()
	base.On("GetStateChan", mock.Anything).Return(make(chan string)).Maybe()
	base.On("Stop").Once()

	runnable := &slowBootRunnable{MockRunnableWithStateable: base, ready: ready}

	pidZero := newTestPIDZero(t,
		WithRunnables(runnable),
		WithStartupTimeout(2*time.Second),
		WithShutdownTimeout(time.Second),
	)

	execDone := startPIDZeroRun(t, pidZero)
	eventuallyClosed(t, runStarted, "Run did not start in time")

	// Inject SIGTERM while the startup loop is still polling IsRunning() —
	// the signal queues in p.signalChan; nothing drains it yet.
	pidZero.SendSignal(syscall.SIGTERM)

	// Release the startup gate so blockUntilRunnableReady returns and Run's
	// startup loop falls through into reap, which drains the buffered
	// SIGTERM and triggers Shutdown.
	close(ready)

	requirePIDZeroRunDone(t, execDone, 3*time.Second)
	base.AssertExpectations(t)
}

// TestPIDZero_Reap_UnhandledSignal tests the default case in signal handling.
func TestPIDZero_Reap_UnhandledSignal(t *testing.T) {
	t.Parallel()

	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("String").Return("unhandledSignalRunnable").Maybe()
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Stop").Once()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a custom signal that's not handled specifically
	pid0, err := New(
		WithContext(ctx),
		WithRunnables(mockRunnable),
		WithSignals(syscall.SIGUSR1), // Custom signal not in switch statement
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Wait for startup and send unhandled signal
	var signalSent bool
	assert.Eventually(t, func() bool {
		if !signalSent {
			pid0.signalChan <- syscall.SIGUSR1 // Should hit default case and continue
			signalSent = true
		}
		return true
	}, 200*time.Millisecond, 10*time.Millisecond, "Should send unhandled signal")

	// Use context cancellation to trigger shutdown cleanly
	cancel()

	// Should complete
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not complete")
	}

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Run_ShutdownSenderInterface tests the ShutdownSender interface path.
func TestPIDZero_Run_ShutdownSenderInterface(t *testing.T) {
	t.Parallel()

	mockShutdownService := mocks.NewMockRunnableWithShutdownSender()
	mockShutdownService.On("String").Return("shutdownSender").Maybe()
	mockShutdownService.On("Run", mock.Anything).Return(nil).Once()
	mockShutdownService.On("Stop").Once()
	mockShutdownService.On("GetShutdownTrigger").Return(make(chan struct{})).Once()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockShutdownService))
	require.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Wait and send shutdown signal
	var signalSent bool
	assert.Eventually(t, func() bool {
		if !signalSent {
			pid0.signalChan <- syscall.SIGINT
			signalSent = true
		}
		return true
	}, 200*time.Millisecond, 10*time.Millisecond, "Should send shutdown signal")

	// Wait for completion
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not finish in time")
	}

	mockShutdownService.AssertExpectations(t)
}

// TestPIDZero_Run_RunnableStartupFailure tests error handling when a runnable fails to start.
func TestPIDZero_Run_RunnableStartupFailure(t *testing.T) {
	t.Parallel()

	// Create a failing stateable runnable
	mockRunnable := mocks.NewMockRunnableWithStateable()
	mockRunnable.On("String").Return("failing-runnable").Maybe()
	mockRunnable.On("IsRunning").Return(false) // Never becomes ready, called multiple times during timeout
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Stop").Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("stopped").Times(3) // Called during startup + shutdown (pre+post)
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Once()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(
		WithContext(ctx),
		WithRunnables(mockRunnable),
		WithStartupTimeout(50*time.Millisecond), // Short timeout to trigger failure
		WithStartupInitial(10*time.Millisecond),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	// Run should return an error due to startup timeout
	err = pid0.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout waiting for runnable to start")

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Shutdown_NoTimeout tests the indefinite wait path when no shutdown timeout is set.
func TestPIDZero_Shutdown_NoTimeout(t *testing.T) {
	t.Parallel()

	mockRunnable := mocks.NewMockRunnableWithStateable()
	mockRunnable.On("String").Return("noTimeoutRunnable").Maybe()
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Stop").Once().After(50 * time.Millisecond) // Small delay to test wait
	mockRunnable.On("IsRunning").Return(true)

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Times(3) // Called during startup and shutdown
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Once()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(
		WithContext(ctx),
		WithRunnables(mockRunnable),
	)
	require.NoError(t, err)
	// Manually set shutdown timeout to 0 to test infinite wait path
	pid0.shutdownTimeout = 0

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Wait and trigger shutdown
	var signalSent bool
	assert.Eventually(t, func() bool {
		if !signalSent {
			pid0.signalChan <- syscall.SIGINT
			signalSent = true
		}
		return true
	}, 200*time.Millisecond, 10*time.Millisecond, "Should send shutdown signal")

	// Should complete without timeout warnings
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not complete")
	}

	mockRunnable.AssertExpectations(t)
}
