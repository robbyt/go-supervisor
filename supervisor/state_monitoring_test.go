package supervisor

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/internal/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPIDZero_StartStateMonitor tests that the state monitor is started for
// stateable runnables and forwards state changes to subscribers. Uses
// testing/synctest by driving startStateMonitor directly (skipping the
// pid0.Run() signal-handling path) so timing is deterministic.
func TestPIDZero_StartStateMonitor(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		mockStateable := mocks.NewMockRunnableWithStateable()
		mockStateable.On("String").Return("stateable-runnable")
		stateChan := make(chan string, 5)
		mockStateable.On("GetStateChan", mock.Anything).Return(stateChan).Once()

		pid0, err := New(WithContext(ctx), WithRunnables(mockStateable))
		require.NoError(t, err)

		// startRunnable seeds the initial state during a real Run(); seed
		// it here directly since we're driving startStateMonitor in isolation.
		pid0.stateMap.Store(mockStateable, "initial")

		stateUpdates := make(chan StateMap, 5)
		unsubscribe := pid0.AddStateSubscriber(stateUpdates)
		defer unsubscribe()

		pid0.wg.Go(pid0.startStateMonitor)
		synctest.Wait()

		// The first state on the channel is discarded by the monitor (it's
		// already captured in stateMap by startRunnable in production).
		stateChan <- "initial"
		stateChan <- "running"
		stateChan <- "stopping"
		synctest.Wait()

		// Drain stateUpdates and confirm the running and stopping states landed.
		var sawRunning, sawStopping bool
		for range len(stateUpdates) {
			stateMap := <-stateUpdates
			switch stateMap["stateable-runnable"] {
			case "running":
				sawRunning = true
			case "stopping":
				sawStopping = true
			}
		}
		assert.True(t, sawRunning, "subscriber should observe 'running' state")
		assert.True(t, sawStopping, "subscriber should observe 'stopping' state")

		cancel()
		synctest.Wait()

		mockStateable.AssertExpectations(t)
	})
}

// TestPIDZero_SubscribeStateChanges tests the SubscribeStateChanges functionality.
func TestPIDZero_SubscribeStateChanges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		mockService := mocks.NewMockRunnableWithStateable()
		stateChan := make(chan string, 2)
		mockService.On("GetStateChan", mock.Anything).Return(stateChan).Once()
		mockService.On("String").Return("mock-service")

		pid0, err := New(WithContext(ctx), WithRunnables(mockService))
		require.NoError(t, err)

		pid0.stateMap.Store(mockService, "initial")

		subCtx, subCancel := context.WithCancel(t.Context())
		defer subCancel()
		stateMapChan := pid0.SubscribeStateChanges(subCtx)

		pid0.wg.Go(pid0.startStateMonitor)
		synctest.Wait()

		stateChan <- "initial"
		stateChan <- "running"

		pid0.stateMap.Store(mockService, "running")
		pid0.broadcastState()

		synctest.Wait()

		var foundRunning bool
		for range len(stateMapChan) {
			stateMap := <-stateMapChan
			if val, ok := stateMap["mock-service"]; ok && val == "running" {
				foundRunning = true
				break
			}
		}
		assert.True(t, foundRunning, "Should have received a state map with running state")

		cancel()

		mockService.AssertExpectations(t)
	})
}

// TestBoundedWaitOnStateGoroutines verifies that the bounded-wait helper
// returns and logs a Warn when the WaitGroup doesn't drain inside
// stateMonitorShutdownTimeout. This exercises the timer.C branch directly
// without depending on a real per-runnable wedge.
func TestBoundedWaitOnStateGoroutines(t *testing.T) {
	t.Parallel()

	const testTimeout = 50 * time.Millisecond

	t.Run("returns within bound when wg drains in time", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			logBuffer := &bytes.Buffer{}
			logHandler := slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})

			stub := mocks.NewMockRunnable()
			stub.On("String").Return("stub").Maybe()
			pid0, err := New(
				WithContext(t.Context()),
				WithRunnables(stub),
				WithLogHandler(logHandler),
				WithStateMonitorShutdownTimeout(testTimeout),
			)
			require.NoError(t, err)

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				time.Sleep(10 * time.Millisecond)
				wg.Done()
			}()

			start := time.Now()
			pid0.boundedWaitOnStateGoroutines(&wg)
			elapsed := time.Since(start)

			assert.Less(t, elapsed, testTimeout,
				"should exit on done branch, not timer")
			assert.Contains(t, logBuffer.String(), "State monitor complete.")
			assert.NotContains(t, logBuffer.String(), "deadline exceeded")
		})
	})

	t.Run("times out and warns when wg blocks past bound", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			logBuffer := &bytes.Buffer{}
			logHandler := slog.NewTextHandler(logBuffer, &slog.HandlerOptions{Level: slog.LevelWarn})

			stub := mocks.NewMockRunnable()
			stub.On("String").Return("stub").Maybe()
			pid0, err := New(
				WithContext(t.Context()),
				WithRunnables(stub),
				WithLogHandler(logHandler),
				WithStateMonitorShutdownTimeout(testTimeout),
			)
			require.NoError(t, err)

			var wg sync.WaitGroup
			wg.Add(1)
			// Helper goroutine parks on `release` until the test signals
			// it; the bound must fire before that signal arrives. Using a
			// channel (not time.Sleep) keeps the helper durably blocked
			// without burning virtual time, and lets the test deterministically
			// drain the bubble at the end.
			release := make(chan struct{})
			go func() {
				<-release
				wg.Done()
			}()

			start := time.Now()
			pid0.boundedWaitOnStateGoroutines(&wg)
			elapsed := time.Since(start)

			assert.Equal(t, testTimeout, elapsed,
				"should exit exactly when the timer fires")
			assert.Contains(t, logBuffer.String(),
				"State monitor shutdown deadline exceeded")

			// Drain the bubble: release the helper so wg.Done() runs,
			// the inner wg.Wait goroutine closes done, and all spawned
			// goroutines exit before the synctest function returns.
			close(release)
		})
	})
}

// TestMonitorStateable_CtxCancelExits verifies that a per-runnable monitor
// goroutine, parked inside the inner select waiting for the next state,
// exits cleanly when the supervisor context is canceled. synctest.Wait
// confirms the goroutine is durably blocked in the select before cancel
// fires, so the test exercises the ctx.Done branch deterministically.
func TestMonitorStateable_CtxCancelExits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())

		s := mocks.NewMockRunnableWithStateable()
		s.On("String").Return("svc").Maybe()
		stateChan := make(chan string, 5)
		s.On("GetStateChan", mock.Anything).Return(stateChan).Once()

		pid0, err := New(WithContext(ctx), WithRunnables(s))
		require.NoError(t, err)

		// First state is consumed and discarded by discardInitialState.
		stateChan <- "initial"

		var wg sync.WaitGroup
		wg.Add(1)
		go pid0.monitorStateable(s, s, &wg)

		// Wait until the monitor has consumed "initial" and is parked in
		// the inner select. With synctest this is durably blocked.
		synctest.Wait()

		cancel()
		wg.Wait() // unblocks when monitorStateable returns and runs its defer

		s.AssertExpectations(t)
	})
}

// TestMonitorStateable_ChannelCloseExits verifies that the monitor goroutine
// exits cleanly when its state channel is closed (the case where a Stateable
// shuts itself down without going through supervisor ctx cancellation).
func TestMonitorStateable_ChannelCloseExits(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		s := mocks.NewMockRunnableWithStateable()
		s.On("String").Return("svc").Maybe()
		stateChan := make(chan string, 5)
		s.On("GetStateChan", mock.Anything).Return(stateChan).Once()

		pid0, err := New(WithContext(ctx), WithRunnables(s))
		require.NoError(t, err)

		stateChan <- "initial"

		var wg sync.WaitGroup
		wg.Add(1)
		go pid0.monitorStateable(s, s, &wg)
		synctest.Wait()

		close(stateChan)
		wg.Wait()

		s.AssertExpectations(t)
	})
}

// TestDiscardInitialState_CtxCancelsDuringWait verifies the helper returns
// false when the supervisor context cancels while it's parked waiting for
// the first state. synctest pins the helper inside the select, then the
// outer goroutine cancels ctx and observes a deterministic return.
func TestDiscardInitialState_CtxCancelsDuringWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())

		stub := mocks.NewMockRunnable()
		stub.On("String").Return("stub").Maybe()
		pid0, err := New(WithContext(ctx), WithRunnables(stub))
		require.NoError(t, err)

		stateChan := make(chan string) // never sends
		r := mocks.NewMockRunnableWithStateable()
		r.On("String").Return("r").Maybe()

		var consumed bool
		done := make(chan struct{})
		go func() {
			consumed = pid0.discardInitialState(r, stateChan)
			close(done)
		}()

		synctest.Wait() // helper goroutine durably blocked in select

		cancel()
		<-done

		assert.False(t, consumed, "should return false when ctx cancels before first state")
	})
}
