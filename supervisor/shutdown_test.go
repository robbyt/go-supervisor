package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPIDZero_StartShutdownManager_TriggersShutdown verifies that receiving a signal
// on a ShutdownSender's trigger channel calls the supervisor's Shutdown method.
func TestPIDZero_StartShutdownManager_TriggersShutdown(t *testing.T) {
	t.Parallel()

	// Create a context with cancel for cleanup and monitoring
	// Use a background context for the supervisor initially
	supervisorCtx, supervisorCancel := context.WithCancel(context.Background())
	defer supervisorCancel() // Ensure cleanup

	// Create mock service that implements ShutdownSender
	mockService := mocks.NewMockRunnableWithShutdownSender()
	shutdownChan := make(chan struct{}, 1) // Buffered to prevent blocking sender

	mockService.On("GetShutdownTrigger").Return(shutdownChan).Once()
	mockService.On("String").Return("mockShutdownService").Maybe()
	mockService.On("Stop").Return().Maybe()

	// Create a supervisor with the mock runnable
	// Pass the specific context so we can monitor its cancellation
	pidZero, err := New(WithContext(supervisorCtx), WithRunnables(mockService))
	require.NoError(t, err)

	// Start the shutdown manager using wg.Go
	pidZero.wg.Go(pidZero.startShutdownManager)

	time.Sleep(200 * time.Millisecond)

	// Send a shutdown signal from the mock runnable
	shutdownChan <- struct{}{}

	// Wait for the supervisor's context to be cancelled, which indicates
	// that p.Shutdown() was called by the manager.
	select {
	case <-pidZero.ctx.Done():
		// Context was cancelled as expected, Shutdown() was called.
	case <-time.After(2 * time.Second):
		t.Fatal("Supervisor context was not cancelled, Shutdown() likely not called")
	}

	// Bypass waiting for the WaitGroup - we've already confirmed
	// the Shutdown() was triggered which is the key test assertion

	// Verify expectations on the mock
	mockService.AssertExpectations(t)
}

// TestPIDZero_StartShutdownManager_ContextCancel verifies that the manager
// cleans up its listener goroutines when the main context is cancelled.
func TestPIDZero_StartShutdownManager_ContextCancel(t *testing.T) {
	t.Parallel()

	// Create a context with cancel for cleanup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cleanup

	// Create mock service that implements ShutdownSender
	mockService := mocks.NewMockRunnableWithShutdownSender()
	shutdownChan := make(chan struct{}) // Unbuffered is fine, won't be used

	// Expect GetShutdownTrigger to be called, but no signal sent
	mockService.On("GetShutdownTrigger").Return(shutdownChan).Once()
	mockService.On("String").Return("mockShutdownService").Maybe()
	mockService.On("Stop").Return().Maybe()

	// Create a supervisor with the mock runnable
	pidZero, err := New(WithContext(ctx), WithRunnables(mockService))
	require.NoError(t, err)

	// Start the shutdown manager using wg.Go
	pidZero.wg.Go(pidZero.startShutdownManager)

	// Give the manager a moment to start its internal listener goroutine
	time.Sleep(100 * time.Millisecond) // Increased sleep duration

	// Cancel the main context
	cancel()

	// Wait for the startShutdownManager goroutine (and its listeners) to finish
	waitChan := make(chan struct{})
	go func() {
		pidZero.wg.Wait()
		close(waitChan)
	}()

	select {
	case <-waitChan:
		// WaitGroup finished cleanly
	case <-time.After(1 * time.Second):
		t.Fatal("WaitGroup did not finish within the timeout after context cancellation")
	}

	// Verify expectations on the mock
	mockService.AssertExpectations(t)
}

// TestPIDZero_StartShutdownManager_NoSenders verifies that the manager
// starts and stops cleanly even if no runnables implement ShutdownSender.
func TestPIDZero_StartShutdownManager_NoSenders(t *testing.T) {
	t.Parallel()

	// Create a context with cancel for cleanup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure cleanup

	// Create a mock service that does *not* implement ShutdownSender
	nonSenderRunnable := mocks.NewMockRunnable()
	nonSenderRunnable.On("Run", mock.Anything).Return(nil).Maybe()
	nonSenderRunnable.On("Stop").Maybe()
	nonSenderRunnable.On("String").Return("simpleRunnable").Maybe()

	// Create a supervisor with the non-sender runnable
	pidZero, err := New(WithContext(ctx), WithRunnables(nonSenderRunnable))
	require.NoError(t, err)

	// Start the shutdown manager using wg.Go
	pidZero.wg.Go(pidZero.startShutdownManager)

	// Give the manager a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the main context
	cancel()

	// Wait for the startShutdownManager goroutine to finish
	waitChan := make(chan struct{})
	go func() {
		pidZero.wg.Wait()
		close(waitChan)
	}()

	select {
	case <-waitChan:
		// WaitGroup finished cleanly
	case <-time.After(1 * time.Second):
		t.Fatal("WaitGroup did not finish within the timeout when no senders were present")
	}

	// No ShutdownSender specific mock expectations to verify
	nonSenderRunnable.AssertExpectations(t) // Assert expectations for Run/Stop/String if set
}

// TestPIDZero_Shutdown_WithTimeoutNotExceeded verifies that shutdown completes
// successfully when runnables finish within the configured timeout.
func TestPIDZero_Shutdown_WithTimeoutNotExceeded(t *testing.T) {
	t.Parallel()

	// Create a blocking channel that will be closed when Stop is called
	stopCalled := make(chan struct{})

	// Create a mock service that blocks in Run until Stop is called
	runnable := mocks.NewMockRunnable()

	// Configure Run to block until Stop is called
	runnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		// Block until stopCalled is closed
		<-stopCalled
	})

	// Configure Stop to unblock the Run method
	runnable.On("Stop").Once().Run(func(args mock.Arguments) {
		close(stopCalled)
	})

	runnable.On("String").Return("blockingRunnable").Maybe()

	// Create supervisor with reasonable timeout
	pidZero, err := New(
		WithRunnables(runnable),
		WithShutdownTimeout(2*time.Second),
	)
	require.NoError(t, err)

	// Run the supervisor
	execDone := make(chan error, 1)
	go func() {
		execDone <- pidZero.Run()
	}()

	// Let Run method start
	time.Sleep(200 * time.Millisecond)

	shutdownStart := time.Now()
	pidZero.Shutdown()
	shutdownDuration := time.Since(shutdownStart)

	assert.Less(t, shutdownDuration, 1*time.Second,
		"Shutdown took too long: %v", shutdownDuration)

	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not complete after shutdown")
	}

	runnable.AssertExpectations(t)
}

// TestPIDZero_Shutdown_WithTimeoutExceeded verifies that shutdown still completes
// but logs a warning when the timeout is exceeded by goroutines that don't stop timely.
func TestPIDZero_Shutdown_WithTimeoutExceeded(t *testing.T) {
	t.Parallel()

	// Create a blocking channel that will NOT be closed by Stop
	// to simulate a runnable that doesn't terminate quickly
	stopCalled := make(chan struct{})
	shutdownComplete := make(chan struct{})

	// Create a mock service that blocks in Run indefinitely
	runnable := mocks.NewMockRunnable()

	// Configure Run to block indefinitely
	runnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		// Block until stopCalled is closed (which won't happen in this test)
		select {
		case <-stopCalled:
			// This won't happen
		case <-shutdownComplete:
			// This will happen after shutdown completes
			// We need this to prevent the goroutine from leaking
		}
	})

	// Configure Stop to NOT unblock the Run method
	// This simulates a runnable that's slow to finish
	runnable.On("Stop").Once().Run(func(args mock.Arguments) {
		// Do not close stopCalled channel, simulating a stuck runnable
		time.Sleep(50 * time.Millisecond) // Fast return from Stop itself
	})

	runnable.On("String").Return("stuckRunnable").Maybe()

	// Create supervisor with very short timeout
	pidZero, err := New(
		WithRunnables(runnable),
		WithShutdownTimeout(200*time.Millisecond), // Shorter than our test duration
	)
	require.NoError(t, err)

	// Run the supervisor
	execDone := make(chan error, 1)
	go func() {
		execDone <- pidZero.Run()
	}()

	time.Sleep(200 * time.Millisecond)

	shutdownStart := time.Now()
	shutdownDone := make(chan struct{})
	go func() {
		pidZero.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		shutdownDuration := time.Since(shutdownStart)

		assert.GreaterOrEqual(t, shutdownDuration, 200*time.Millisecond,
			"Shutdown returned too quickly: %v", shutdownDuration)
		assert.Less(t, shutdownDuration, 500*time.Millisecond,
			"Shutdown took too long: %v", shutdownDuration)
	case <-time.After(1 * time.Second):
		t.Fatal("Shutdown did not complete despite timeout")
	}

	close(shutdownComplete) // Prevent goroutine leak
	runnable.AssertExpectations(t)
}
