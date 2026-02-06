package supervisor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPIDZero_StartShutdownManager_TriggersShutdown verifies that receiving a signal
// on a ShutdownSender's trigger channel calls the supervisor's Shutdown method.
// Cannot use synctest: the listener goroutine calls Shutdown() which calls wg.Wait()
// on the same WaitGroup that includes startShutdownManager, creating a circular dependency
// that synctest's "all goroutines must complete" requirement turns into a deadlock.
func TestPIDZero_StartShutdownManager_TriggersShutdown(t *testing.T) {
	t.Parallel()

	supervisorCtx, supervisorCancel := context.WithCancel(context.Background())
	defer supervisorCancel()

	mockService := mocks.NewMockRunnableWithShutdownSender()
	shutdownChan := make(chan struct{}, 1)

	mockService.On("GetShutdownTrigger").Return(shutdownChan).Once()
	mockService.On("String").Return("mockShutdownService").Maybe()
	mockService.On("Stop").Return().Maybe()

	pidZero, err := New(WithContext(supervisorCtx), WithRunnables(mockService))
	require.NoError(t, err)

	pidZero.wg.Go(pidZero.startShutdownManager)

	shutdownChan <- struct{}{}

	select {
	case <-pidZero.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Supervisor context was not cancelled, Shutdown() likely not called")
	}

	mockService.AssertExpectations(t)
}

// TestPIDZero_StartShutdownManager_ContextCancel verifies that the manager
// cleans up its listener goroutines when the main context is cancelled.
func TestPIDZero_StartShutdownManager_ContextCancel(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		mockService := mocks.NewMockRunnableWithShutdownSender()
		shutdownChan := make(chan struct{})

		mockService.On("GetShutdownTrigger").Return(shutdownChan).Once()
		mockService.On("String").Return("mockShutdownService").Maybe()
		mockService.On("Stop").Return().Maybe()

		pidZero, err := New(WithContext(ctx), WithRunnables(mockService))
		require.NoError(t, err)

		pidZero.wg.Go(pidZero.startShutdownManager)

		cancel()
		synctest.Wait()

		mockService.AssertExpectations(t)
	})
}

// TestPIDZero_StartShutdownManager_NoSenders verifies that the manager
// starts and stops cleanly even if no runnables implement ShutdownSender.
func TestPIDZero_StartShutdownManager_NoSenders(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		nonSenderRunnable := mocks.NewMockRunnable()
		nonSenderRunnable.On("Run", mock.Anything).Return(nil).Maybe()
		nonSenderRunnable.On("Stop").Maybe()
		nonSenderRunnable.On("String").Return("simpleRunnable").Maybe()

		pidZero, err := New(WithContext(ctx), WithRunnables(nonSenderRunnable))
		require.NoError(t, err)

		pidZero.wg.Go(pidZero.startShutdownManager)

		cancel()
		synctest.Wait()

		nonSenderRunnable.AssertExpectations(t)
	})
}

// TestPIDZero_Shutdown_WithTimeoutNotExceeded verifies that shutdown completes
// successfully when runnables finish within the configured timeout.
// Cannot use synctest: calls pidZero.Run() which uses signal.Notify.
func TestPIDZero_Shutdown_WithTimeoutNotExceeded(t *testing.T) {
	t.Parallel()

	stopCalled := make(chan struct{})

	runnable := mocks.NewMockRunnable()
	runStarted := make(chan struct{})

	runnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		close(runStarted)
		<-stopCalled
	})

	runnable.On("Stop").Once().Run(func(args mock.Arguments) {
		close(stopCalled)
	})

	runnable.On("String").Return("blockingRunnable").Maybe()

	pidZero, err := New(
		WithRunnables(runnable),
		WithShutdownTimeout(2*time.Second),
	)
	require.NoError(t, err)

	execDone := make(chan error, 1)
	go func() {
		execDone <- pidZero.Run()
	}()

	select {
	case <-runStarted:
	case <-time.After(time.Second):
		t.Fatal("Run did not start in time")
	}

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
// Cannot use synctest: calls pidZero.Run() which uses signal.Notify.
func TestPIDZero_Shutdown_WithTimeoutExceeded(t *testing.T) {
	t.Parallel()

	stopCalled := make(chan struct{})
	shutdownComplete := make(chan struct{})

	runnable := mocks.NewMockRunnable()

	runStarted := make(chan struct{})
	runnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		close(runStarted)
		select {
		case <-stopCalled:
		case <-shutdownComplete:
		}
	})

	runnable.On("Stop").Once().Run(func(args mock.Arguments) {
		time.Sleep(50 * time.Millisecond)
	})

	runnable.On("String").Return("stuckRunnable").Maybe()

	pidZero, err := New(
		WithRunnables(runnable),
		WithShutdownTimeout(200*time.Millisecond),
	)
	require.NoError(t, err)

	execDone := make(chan error, 1)
	go func() {
		execDone <- pidZero.Run()
	}()

	select {
	case <-runStarted:
	case <-time.After(time.Second):
		t.Fatal("Run did not start in time")
	}

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

	close(shutdownComplete)
	runnable.AssertExpectations(t)
}
