package supervisor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/internal/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPIDZero_Shutdown(t *testing.T) {
	t.Parallel()

	t.Run("shutdown sender trigger completes without deadlock", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockService := mocks.NewMockRunnableWithShutdownSender()
			shutdownChan := make(chan struct{}, 1)

			mockService.On("GetShutdownTrigger").Return(shutdownChan).Once()
			mockService.On("String").Return("mockShutdownService")
			mockService.On("Stop").Return().Once()

			pidZero := newTestPIDZero(t, WithContext(ctx), WithRunnables(mockService))
			pidZero.wg.Go(pidZero.startShutdownManager)

			shutdownChan <- struct{}{}
			synctest.Wait()

			// With the fix, the listener cancels the supervisor ctx
			// instead of running Shutdown itself, so it returns
			// promptly and shutdownWg drains. The test's Shutdown call
			// below picks up shutdownOnce and runs the body
			// uncontested. Without the fix, the listener was stuck
			// inside Shutdown's p.wg.Wait while startShutdownManager
			// waited on the listener via shutdownWg.Wait — circular
			// wait — and a second Shutdown call would block on
			// shutdownOnce indefinitely.
			shutdownDone := make(chan struct{})
			go func() {
				pidZero.Shutdown()
				close(shutdownDone)
			}()
			synctest.Wait()

			select {
			case <-shutdownDone:
			default:
				t.Fatal("Shutdown blocked: circular wait between listener and startShutdownManager")
			}

			mockService.AssertExpectations(t)
		})
	})

	// Cannot use synctest: this calls pidZero.Run(), which uses signal.Notify.
	// Exercises the production dispatch path (listener cancels p.ctx → reap
	// picks up <-p.ctx.Done() → Shutdown). The other synctest test above
	// drives startShutdownManager + Shutdown directly and would still pass if
	// the listener's p.cancel() somehow stopped reaching reap.
	t.Run("shutdown sender trigger drives Run loop to clean exit", func(t *testing.T) {
		runnable := mocks.NewMockRunnableWithShutdownSender()
		shutdownChan := make(chan struct{}, 1)
		runStarted := make(chan struct{})

		runnable.On("GetShutdownTrigger").Return(shutdownChan).Once()
		runnable.On("String").Return("shutdownSenderRunnable").Maybe()
		runnable.On("Run", mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			close(runStarted)
			<-ctx.Done()
		})
		runnable.On("Stop").Once()

		pidZero := newTestPIDZero(t,
			WithRunnables(runnable),
			WithShutdownTimeout(time.Second),
		)

		execDone := startPIDZeroRun(t, pidZero)
		eventuallyClosed(t, runStarted, "Run did not start in time")

		shutdownChan <- struct{}{}

		requirePIDZeroRunDone(t, execDone, time.Second)
		runnable.AssertExpectations(t)
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

// TestPIDZero_ShutdownSender_DuringReload covers Codex stability gap #7: a
// ShutdownSender trigger fires while a reload is in flight. With the T1.3
// channel-dispatch fix the listener cancels p.ctx and reap's existing
// <-p.ctx.Done() arm invokes Shutdown — so an in-flight Reload doesn't wedge
// the shutdown handoff. Pre-T1.3, the listener called Shutdown() inline,
// which created a circular wait (listener inside Shutdown's p.wg.Wait while
// startShutdownManager waited on the listener via shutdownWg.Wait); T1.2's
// shutdownTimeout bounded that wedge but logged "Shutdown timeout exceeded".
// We pin the post-T1.3 contract — Shutdown must complete promptly, not
// burn the full shutdownTimeout — by making the configured budget small and
// asserting Run returns well within it.
//
// Cannot use synctest: this calls pidZero.Run(), which uses signal.Notify.
func TestPIDZero_ShutdownSender_DuringReload(t *testing.T) {
	t.Parallel()

	const shutdownBudget = 500 * time.Millisecond

	runnable := mocks.NewMockRunnableWithShutdownSender()
	shutdownChan := make(chan struct{}, 1)
	runStarted := make(chan struct{})
	reloadStarted := make(chan struct{})
	releaseReload := make(chan struct{})

	runnable.On("GetShutdownTrigger").Return(shutdownChan).Once()
	runnable.On("String").Return("shutdownReloadRunnable").Maybe()
	runnable.On("Run", mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		close(runStarted)
		<-ctx.Done()
	})
	runnable.On("Reload", mock.Anything).Return(nil).Once().Run(func(args mock.Arguments) {
		close(reloadStarted)
		<-releaseReload
	})
	runnable.On("Stop").Once()

	pidZero := newTestPIDZero(t,
		WithRunnables(runnable),
		WithShutdownTimeout(shutdownBudget),
	)

	execDone := startPIDZeroRun(t, pidZero)
	eventuallyClosed(t, runStarted, "Run did not start in time")

	// Kick off a reload and wait until the mock confirms it's actually
	// inside Reload (not just queued by the manager). At that point the
	// reload-manager goroutine is blocked on releaseReload.
	go pidZero.ReloadAll()
	eventuallyClosed(t, reloadStarted, "Reload did not start in time")

	// Fire the shutdown trigger while Reload is mid-call. The listener
	// cancels p.ctx; reap's <-p.ctx.Done() arm dispatches Shutdown.
	shutdownStart := time.Now()
	shutdownChan <- struct{}{}

	// Release the reload so the reload-manager goroutine can return and
	// observe its own ctx.Done(). Shutdown's wg.Wait must not wedge.
	close(releaseReload)

	requirePIDZeroRunDone(t, execDone, time.Second)
	shutdownDuration := time.Since(shutdownStart)

	// With T1.3 shutdown should complete promptly — well inside the
	// configured budget. A regression that re-introduces the circular wait
	// would push this near or past the budget (bounded by T1.2's deadline).
	require.Less(t, shutdownDuration, shutdownBudget/2,
		"Shutdown took %v of %v budget — circular wait between listener and shutdown manager?",
		shutdownDuration, shutdownBudget)
	runnable.AssertExpectations(t)
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
