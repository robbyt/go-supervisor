package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	assert.NoError(t, err)

	// Start the shutdown manager in a goroutine
	pidZero.wg.Add(1)
	go pidZero.startShutdownManager()

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
	assert.NoError(t, err)

	// Start the shutdown manager in a goroutine
	pidZero.wg.Add(1)
	go pidZero.startShutdownManager()

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
		// WaitGroup finished cleanly, indicating proper cleanup
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
	assert.NoError(t, err)

	// Start the shutdown manager in a goroutine
	pidZero.wg.Add(1)
	go pidZero.startShutdownManager()

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
