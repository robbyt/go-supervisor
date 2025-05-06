package supervisor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
	assert.NoError(t, err)

	assert.NotNil(t, pid0)
	assert.Equal(t, 1, len(pid0.runnables))

	assert.Equal(t, pid0.String(), "Supervisor<runnables: 1>")
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
	assert.NoError(t, err)

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
	assert.NoError(t, err)
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
	assert.NoError(t, err)

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
	assert.NoError(t, err)
	assert.Len(t, defaultPid0.subscribeSignals, 3)
	assert.Contains(t, defaultPid0.subscribeSignals, syscall.SIGINT)
	assert.Contains(t, defaultPid0.subscribeSignals, syscall.SIGTERM)
	assert.Contains(t, defaultPid0.subscribeSignals, syscall.SIGHUP)
}

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
	assert.NoError(t, err)

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
	assert.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Allow some time for services to start
	time.Sleep(100 * time.Millisecond)

	// Send SIGINT signal
	pid0.SignalChan <- syscall.SIGINT

	// Wait for Exec to finish due to SIGINT
	select {
	case err := <-execDone:
		assert.NoError(t, err)
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
	assert.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Allow some time for services to start
	time.Sleep(10 * time.Millisecond)

	// Send SIGTERM signal
	pid0.SignalChan <- syscall.SIGTERM

	// Wait for Exec to finish due to SIGTERM
	select {
	case err := <-execDone:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGTERM")
	}

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Reap_HandleSIGHUP tests that receiving SIGHUP triggers reload.
func TestPIDZero_Reap_HandleSIGHUP(t *testing.T) {
	t.Parallel()
	mockRunnable := new(mocks.Runnable)
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable.On("Reload").Once()
	mockRunnable.On("Stop").Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	assert.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Allow some time for services to start
	time.Sleep(10 * time.Millisecond)

	// Send SIGHUP signal
	pid0.SignalChan <- syscall.SIGHUP

	// Allow some time for reload to occur
	time.Sleep(100 * time.Millisecond)

	// Send SIGINT to shutdown
	pid0.SignalChan <- syscall.SIGINT

	// Wait for Exec to finish
	select {
	case err := <-execDone:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGHUP and SIGINT")
	}

	mockRunnable.AssertExpectations(t)
}

// TestPIDZero_Reap_MultipleSignals tests handling multiple signals.
func TestPIDZero_Reap_MultipleSignals(t *testing.T) {
	t.Parallel()
	mockRunnable1 := new(mocks.Runnable)
	mockRunnable2 := new(mocks.Runnable)
	mockRunnable1.On("Run", mock.Anything).Return(nil).Once()
	mockRunnable2.On("Run", mock.Anything).Return(nil).Once()

	// TODO: these should be called by the hup, need to investigate!
	mockRunnable1.On("Reload").Once()
	mockRunnable2.On("Reload").Once()

	mockRunnable1.On("Stop").Once()
	mockRunnable2.On("Stop").Once()

	// Setup for Stateable interface
	stateChan1 := make(chan string)
	stateChan2 := make(chan string)
	mockRunnable1.On("GetState").Return("running").Maybe()
	mockRunnable2.On("GetState").Return("running").Maybe()
	mockRunnable1.On("GetStateChan", mock.Anything).Return(stateChan1).Maybe()
	mockRunnable2.On("GetStateChan", mock.Anything).Return(stateChan2).Maybe()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable1, mockRunnable2))
	assert.NoError(t, err)

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		// Start the blocking supervisor in the background
		execDone <- pid0.Run()
	}()

	// Send SIGHUP signal
	pid0.SignalChan <- syscall.SIGHUP

	time.Sleep(100 * time.Millisecond)

	// Send SIGTERM signal
	pid0.SignalChan <- syscall.SIGTERM

	// Wait for Exec to finish due to SIGTERM
	select {
	case err := <-execDone:
		assert.NoError(t, err)
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
	assert.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Send SIGINT signal to shutdown
	pid0.SignalChan <- syscall.SIGINT

	// Wait for Exec to finish
	select {
	case err := <-execDone:
		assert.NoError(t, err)
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
	assert.NoError(t, err)

	t.Cleanup(func() {
		pid0.Shutdown()
	})

	pid0.listenForSignals()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Allow some time for services to start
	time.Sleep(100 * time.Millisecond)

	// Send two SIGINT signals
	pid0.SignalChan <- syscall.SIGINT
	pid0.SignalChan <- syscall.SIGINT

	// Wait for Exec to finish
	select {
	case err := <-execDone:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after multiple SIGINT")
	}

	// Ensure Shutdown was called only once
	mockRunnable.AssertNumberOfCalls(t, "Stop", 1)
}

// TestPIDZero_ShutdownIgnoresSIGHUP tests that SIGHUP signals are ignored during shutdown.
func TestPIDZero_ShutdownIgnoresSIGHUP(t *testing.T) {
	t.Parallel()
	// Create a mock runnable
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.DelayStop = 500 * time.Millisecond
	mockRunnable.DelayRun = 1 * time.Millisecond
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	// Expect Reload not to be called
	// Stop should be called once during shutdown
	mockRunnable.On("Stop").Once()
	mockRunnable.On("Reload").Panic("Reload should not be called")

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	// Create PIDZero with the mock runnable
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	assert.NoError(t, err)

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
		pid0.SignalChan <- syscall.SIGTERM
	}()

	go func() {
		defer wg.Done()
		<-trigger2
		pid0.SignalChan <- syscall.SIGHUP
	}()

	// Allow some time for services to start
	time.Sleep(100 * time.Millisecond)

	// Trigger both goroutines to proceed
	close(trigger1)
	time.Sleep(100 * time.Microsecond)
	close(trigger2)

	// Wait for both goroutines to finish
	wg.Wait()

	// Wait for Exec to finish due to SIGTERM
	select {
	case err := <-execDone:
		assert.NoError(t, err)
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
	mockRunnable.DelayStop = 500 * time.Millisecond
	mockRunnable.DelayRun = 1 * time.Millisecond
	mockRunnable.On("Run", mock.Anything).Return(nil).Once()
	// Expect Reload not to be called
	// Stop should be called once during shutdown
	mockRunnable.On("Stop").Once()

	// Setup for Stateable interface
	stateChan := make(chan string)
	mockRunnable.On("GetState").Return("running").Maybe()
	mockRunnable.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

	// Create PIDZero with the mock runnable
	ctx, cancel := context.WithCancel(context.Background())
	pid0, err := New(WithContext(ctx), WithRunnables(mockRunnable))
	assert.NoError(t, err)
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
		assert.NoError(t, err)
		t.Log("Exec finished without errors")
	case <-time.After(3 * time.Second):
		t.Fatal("Exec did not finish in time after SIGTERM and SIGHUP")
	}

	// Ensure Reload was not called
	mockRunnable.AssertNotCalled(t, "Reload")

	// Ensure Stop was called only once
	mockRunnable.AssertNumberOfCalls(t, "Stop", 1)
}

func TestBlockUntilRunnableReady(t *testing.T) {
	t.Parallel()

	t.Run("immediately ready", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnableWithStatable()
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
		mockRunnable := mocks.NewMockRunnableWithStatable()
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
		mockRunnable := mocks.NewMockRunnableWithStatable()
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

		assert.Error(t, resultErr)
		assert.Contains(t, resultErr.Error(), "timeout waiting for runnable to start")
		mockRunnable.AssertExpectations(t)
	})

	t.Run("parent context canceled", func(t *testing.T) {
		// Setup mock that never reports as running
		mockRunnable := mocks.NewMockRunnableWithStatable()
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

		// Cancel the context right away
		cancel()

		// Use Eventually to verify the correct result
		result := assert.Eventually(t, func() bool {
			err := sv.blockUntilRunnableReady(mockRunnable)
			return err == nil // Should return nil when parent context is canceled
		}, 200*time.Millisecond, 10*time.Millisecond, "Should return nil when context is canceled")

		assert.True(t, result, "blockUntilRunnableReady should return nil when context is canceled")
		mockRunnable.AssertExpectations(t)
	})

	t.Run("error from runnable", func(t *testing.T) {
		// Setup mock that never reports as running
		mockRunnable := mocks.NewMockRunnableWithStatable()
		mockRunnable.On("String").Return("error-runnable").Maybe()
		mockRunnable.On("IsRunning").Return(false).Maybe() // Always returns false

		sv, err := New(
			WithRunnables(mockRunnable),
			WithStartupTimeout(500*time.Millisecond),
			WithStartupInitial(10*time.Millisecond),
		)
		require.NoError(t, err)

		// Send an error through the error channel
		expectedErr := errors.New("runnable startup error")
		go func() {
			time.Sleep(20 * time.Millisecond)
			sv.errorChan <- expectedErr
		}()

		// Call blockUntilRunnableReady - should return the error
		var resultErr error
		assert.Eventually(t, func() bool {
			resultErr = sv.blockUntilRunnableReady(mockRunnable)
			return resultErr != nil
		}, 200*time.Millisecond, 10*time.Millisecond, "Should return error from error channel")

		assert.Error(t, resultErr)
		assert.Contains(t, resultErr.Error(), "runnable failed to start")
		assert.Contains(t, resultErr.Error(), expectedErr.Error())

		// Verify the error was put back in the channel for reap() to process
		select {
		case err := <-sv.errorChan:
			assert.Equal(t, expectedErr, err, "Error should be put back in channel")
		default:
			t.Fatal("Error was not put back in the channel")
		}

		mockRunnable.AssertExpectations(t)
	})
}

// TestPIDZero_Run_StateMonitor tests that the state monitor is started for stateable runnables
func TestPIDZero_Run_StateMonitor(t *testing.T) {
	t.Parallel()

	// Create a mock stateable runnable
	mockStateable := mocks.NewMockRunnableWithStatable()
	mockStateable.On("String").Return("stateable-runnable").Maybe()
	mockStateable.On("Run", mock.Anything).Return(nil)
	mockStateable.On("Stop").Once()
	mockStateable.On("GetState").Return("initial").Once()  // Initial state
	mockStateable.On("GetState").Return("running").Maybe() // Called during shutdown

	stateChan := make(chan string, 5) // Buffered to prevent blocking
	mockStateable.On("GetStateChan", mock.Anything).Return(stateChan).Once()

	// Will be called during startup verification
	mockStateable.On("IsRunning").Return(true).Once()

	// Create context with timeout to ensure test completion
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create supervisor with the mock runnable
	pid0, err := New(
		WithContext(ctx),
		WithRunnables(mockStateable),
		WithStartupTimeout(100*time.Millisecond),
	)
	require.NoError(t, err)

	// Create a state subscriber to verify state broadcasts
	stateUpdates := make(chan StateMap, 5)
	pid0.AddStateSubscriber(stateUpdates)

	// Start the supervisor in a goroutine
	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Allow time for initialization
	time.Sleep(50 * time.Millisecond)

	// Send state updates through the channel
	stateChan <- "initial"  // This should be discarded as it's the initial state
	stateChan <- "running"  // This will be processed
	stateChan <- "stopping" // Additional state change

	// Use require.Eventually to verify the state monitor receives and broadcasts states
	require.Eventually(t, func() bool {
		// Check if we have received at least one state update
		select {
		case stateMap := <-stateUpdates:
			// We don't check for specific values, just that broadcasts are happening
			return stateMap["stateable-runnable"] != ""
		default:
			return false
		}
	}, 500*time.Millisecond, 50*time.Millisecond, "No state updates received")

	// Cancel the context to shut down the supervisor
	cancel()

	// Verify the supervisor shuts down cleanly
	select {
	case err := <-execDone:
		assert.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Supervisor did not shut down in time")
	}

	// Verify expectations
	mockStateable.AssertExpectations(t)
}
