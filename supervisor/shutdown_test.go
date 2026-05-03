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

func TestPIDZero_Shutdown(t *testing.T) {
	t.Parallel()

	// Cannot use synctest: the listener goroutine calls Shutdown() which calls
	// wg.Wait() on the same WaitGroup that includes startShutdownManager,
	// creating a circular dependency that synctest's "all goroutines must
	// complete" requirement turns into a deadlock.
	t.Run("shutdown sender trigger calls shutdown", func(t *testing.T) {
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

		pidZero := newTestPIDZero(t, WithContext(supervisorCtx), WithRunnables(mockService))
		pidZero.wg.Go(pidZero.startShutdownManager)

		shutdownChan <- struct{}{}
		eventuallyClosed(t, stopCalled, "Stop() was not called, Shutdown() likely not invoked")

		mockService.AssertExpectations(t)
	})

	t.Run("shutdown manager exits on context cancel", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockService := mocks.NewMockRunnableWithShutdownSender()
			shutdownChan := make(chan struct{})

			mockService.On("GetShutdownTrigger").Return(shutdownChan).Once()

			pidZero := newTestPIDZero(t, WithContext(ctx), WithRunnables(mockService))
			pidZero.wg.Go(pidZero.startShutdownManager)

			cancel()
			synctest.Wait()

			mockService.AssertExpectations(t)
		})
	})

	t.Run("shutdown manager handles no senders", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			nonSenderRunnable := mocks.NewMockRunnable()

			pidZero := newTestPIDZero(t, WithContext(ctx), WithRunnables(nonSenderRunnable))
			pidZero.wg.Go(pidZero.startShutdownManager)

			cancel()
			synctest.Wait()

			nonSenderRunnable.AssertExpectations(t)
		})
	})

	// Cannot use synctest: this calls pidZero.Run(), which uses signal.Notify.
	t.Run("shutdown completes before timeout", func(t *testing.T) {
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

		pidZero := newTestPIDZero(t,
			WithRunnables(runnable),
			WithShutdownTimeout(2*time.Second),
		)

		execDone := startPIDZeroRun(t, pidZero)
		eventuallyClosed(t, runStarted, "Run did not start in time")

		shutdownStart := time.Now()
		pidZero.Shutdown()
		shutdownDuration := time.Since(shutdownStart)

		assert.Less(t, shutdownDuration, time.Second,
			"Shutdown took too long: %v", shutdownDuration)
		requirePIDZeroRunDone(t, execDone, 200*time.Millisecond)

		runnable.AssertExpectations(t)
	})

	// Cannot use synctest: this calls pidZero.Run(), which uses signal.Notify.
	t.Run("shutdown times out waiting for goroutines", func(t *testing.T) {
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

		pidZero := newTestPIDZero(t,
			WithRunnables(runnable),
			WithShutdownTimeout(200*time.Millisecond),
		)

		_ = startPIDZeroRun(t, pidZero)
		eventuallyClosed(t, runStarted, "Run did not start in time")

		shutdownStart := time.Now()
		shutdownDone := make(chan struct{})
		go func() {
			pidZero.Shutdown()
			close(shutdownDone)
		}()

		eventuallyClosed(t, shutdownDone, "Shutdown did not complete despite timeout")
		shutdownDuration := time.Since(shutdownStart)

		assert.GreaterOrEqual(t, shutdownDuration, 200*time.Millisecond,
			"Shutdown returned too quickly: %v", shutdownDuration)
		assert.Less(t, shutdownDuration, 500*time.Millisecond,
			"Shutdown took too long: %v", shutdownDuration)

		close(shutdownComplete)
		runnable.AssertExpectations(t)
	})

	t.Run("hung stop is bounded by deadline", func(t *testing.T) {
		hangForever := make(chan struct{})
		t.Cleanup(func() {
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

		pidZero := newTestPIDZero(t,
			WithRunnables(runnable),
			WithShutdownTimeout(150*time.Millisecond),
		)

		_ = startPIDZeroRun(t, pidZero)
		eventuallyClosed(t, runStarted, "Run did not start in time")

		shutdownStart := time.Now()
		shutdownDone := make(chan struct{})
		go func() {
			pidZero.Shutdown()
			close(shutdownDone)
		}()

		eventuallyClosed(t, shutdownDone, "Shutdown blocked despite timeout; total-deadline fix not active")
		elapsed := time.Since(shutdownStart)

		assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond,
			"Shutdown returned too quickly: %v", elapsed)
		assert.Less(t, elapsed, time.Second,
			"Shutdown should return near the deadline despite hung Stop(); got %v", elapsed)
	})

	t.Run("abandoned runnable can return error after shutdown", func(t *testing.T) {
		releaseRun := make(chan struct{})
		releaseStop := make(chan struct{})
		t.Cleanup(func() {
			close(releaseStop)
		})

		runnable := mocks.NewMockRunnable()
		runStarted := make(chan struct{})
		runnable.On("Run", mock.Anything).Return(assert.AnError).Run(func(args mock.Arguments) {
			close(runStarted)
			<-releaseRun
		})
		runnable.On("Stop").Run(func(args mock.Arguments) {
			<-releaseStop
		})

		pidZero := newTestPIDZero(t,
			WithRunnables(runnable),
			WithShutdownTimeout(50*time.Millisecond),
		)

		execDone := startPIDZeroRun(t, pidZero)
		eventuallyClosed(t, runStarted, "Run did not start in time")

		shutdownDone := make(chan struct{})
		go func() {
			pidZero.Shutdown()
			close(shutdownDone)
		}()

		eventuallyClosed(t, shutdownDone, "Shutdown did not complete after abandoning Stop()")

		close(releaseRun)

		assert.Eventually(t, func() bool {
			return len(pidZero.errorChan) == 1
		}, time.Second, 10*time.Millisecond)

		select {
		case err := <-execDone:
			require.NoError(t, err)
		default:
		}

		runnable.AssertExpectations(t)
	})

	t.Run("abandoned stop does not cache post state", func(t *testing.T) {
		releaseStop := make(chan struct{})
		t.Cleanup(func() {
			close(releaseStop)
		})

		runnable := mocks.NewMockRunnableWithStateable()
		runnable.On("String").Return("stateable-runnable").Maybe()
		runnable.On("GetState").Return("running").Once()
		runnable.On("Stop").Run(func(args mock.Arguments) {
			<-releaseStop
		})

		pidZero := newTestPIDZero(t,
			WithRunnables(runnable),
			WithShutdownTimeout(50*time.Millisecond),
		)
		pidZero.stateMap.Store(runnable, "cached-running")

		shutdownDone := make(chan struct{})
		go func() {
			pidZero.Shutdown()
			close(shutdownDone)
		}()

		eventuallyClosed(t, shutdownDone, "Shutdown did not complete after abandoning Stop()")

		cachedState, ok := pidZero.stateMap.Load(runnable)
		require.True(t, ok)
		assert.Equal(t, "cached-running", cachedState)
		runnable.AssertExpectations(t)
	})

	t.Run("completed stop caches post state", func(t *testing.T) {
		runnable := mocks.NewMockRunnableWithStateable()
		runnable.On("String").Return("stateable-runnable").Maybe()
		runnable.On("GetState").Return("running").Once()
		runnable.On("GetState").Return("stopped").Once()
		runnable.On("Stop").Once()

		pidZero := newTestPIDZero(t,
			WithRunnables(runnable),
			WithShutdownTimeout(time.Second),
		)
		pidZero.stateMap.Store(runnable, "cached-running")

		pidZero.Shutdown()

		cachedState, ok := pidZero.stateMap.Load(runnable)
		require.True(t, ok)
		assert.Equal(t, "stopped", cachedState)
		runnable.AssertExpectations(t)
	})

	t.Run("stop runnable bounded completes before deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			runnable := mocks.NewMockRunnable()
			runnable.On("Stop").Once()

			pidZero := newTestPIDZero(t, WithRunnables(runnable))

			stopped := pidZero.stopRunnableBounded(
				runnable,
				time.Now().Add(time.Second),
				time.Now(),
			)

			assert.True(t, stopped)
			runnable.AssertExpectations(t)
		})
	})

	t.Run("stop runnable bounded deadline expires", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			releaseStop := make(chan struct{})

			runnable := mocks.NewMockRunnable()
			runnable.On("Stop").Run(func(args mock.Arguments) {
				<-releaseStop
			})

			pidZero := newTestPIDZero(t,
				WithRunnables(runnable),
				WithShutdownTimeout(time.Second),
			)

			stopped := pidZero.stopRunnableBounded(
				runnable,
				time.Now().Add(time.Second),
				time.Now(),
			)
			assert.False(t, stopped)

			close(releaseStop)
			synctest.Wait()
			runnable.AssertExpectations(t)
		})
	})

	t.Run("stop runnable bounded deadline already expired", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			releaseStop := make(chan struct{})

			runnable := mocks.NewMockRunnable()
			runnable.On("Stop").Run(func(args mock.Arguments) {
				<-releaseStop
			})

			pidZero := newTestPIDZero(t, WithRunnables(runnable))

			stopped := pidZero.stopRunnableBounded(
				runnable,
				time.Now().Add(-time.Nanosecond),
				time.Now(),
			)
			assert.False(t, stopped)

			close(releaseStop)
			synctest.Wait()
			runnable.AssertExpectations(t)
		})
	})
}

func newTestPIDZero(t *testing.T, opts ...Option) *PIDZero {
	t.Helper()

	pidZero, err := New(opts...)
	require.NoError(t, err)
	return pidZero
}

func startPIDZeroRun(t *testing.T, pidZero *PIDZero) <-chan error {
	t.Helper()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pidZero.Run()
	}()
	return execDone
}

func eventuallyClosed(t *testing.T, ch <-chan struct{}, message string) {
	t.Helper()

	assert.Eventually(t, func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond, message)
}

func requirePIDZeroRunDone(t *testing.T, execDone <-chan error, timeout time.Duration) {
	t.Helper()

	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(timeout):
		t.Fatal("Run did not complete after shutdown")
	}
}
