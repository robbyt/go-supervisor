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
	stopCalled := make(chan struct{})

	mockService.On("GetShutdownTrigger").Return(shutdownChan).Once()
	mockService.On("String").Return("mockShutdownService")
	mockService.On("Stop").Return().Once().Run(func(args mock.Arguments) {
		close(stopCalled)
	})

	pidZero, err := New(WithContext(supervisorCtx), WithRunnables(mockService))
	require.NoError(t, err)

	pidZero.wg.Go(pidZero.startShutdownManager)

	shutdownChan <- struct{}{}

	select {
	case <-stopCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() was not called, Shutdown() likely not invoked")
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

// TestPIDZero_Shutdown_HungStopBoundedByDeadline verifies that a runnable
// whose Stop() never returns does NOT wedge the supervisor's Shutdown()
// indefinitely. The total shutdown timeout bounds Stop() calls; on expiry,
// the goroutine is abandoned and shutdown proceeds.
//
// Regression: prior to the total-deadline fix, Shutdown() called Stop()
// synchronously and the timeout only bounded the post-Stop wg.Wait(). A
// single hung Stop() blocked shutdown forever before the timeout engaged.
func TestPIDZero_Shutdown_HungStopBoundedByDeadline(t *testing.T) {
	t.Parallel()

	hangForever := make(chan struct{}) // never closed by the test path
	t.Cleanup(func() {
		// Release the orphaned Stop goroutine so the test's runnable
		// goroutines can exit cleanly when the test framework tears down.
		close(hangForever)
	})

	runnable := mocks.NewMockRunnable()
	runStarted := make(chan struct{})
	runnable.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		close(runStarted)
		<-hangForever
	})
	runnable.On("Stop").Run(func(args mock.Arguments) {
		<-hangForever
	})

	pidZero, err := New(
		WithRunnables(runnable),
		WithShutdownTimeout(150*time.Millisecond),
	)
	require.NoError(t, err)

	execDone := make(chan error, 1)
	go func() { execDone <- pidZero.Run() }()

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
		elapsed := time.Since(shutdownStart)
		assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond,
			"Shutdown returned too quickly: %v", elapsed)
		assert.Less(t, elapsed, 1*time.Second,
			"Shutdown should return near the deadline despite hung Stop(); got %v", elapsed)
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown blocked despite timeout — total-deadline fix not active")
	}
}
