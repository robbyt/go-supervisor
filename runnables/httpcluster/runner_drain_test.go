package httpcluster

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/mocks"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestShutdownDrainsConfigSiphon verifies that external goroutines parked in a
// send on the public siphon channel are unblocked when the runner shuts down.
// Without the drain, a parked sender would block forever because the main
// event loop has exited and nothing is reading the channel.
//
// The test forces senders to park by using a slow runner factory: while the
// event loop is in createAndStartServer the unbuffered siphon has no reader,
// so any concurrent send blocks until either the reader returns to the select
// or the drain consumes the value. Under synctest the slow factory's sleep
// is synthetic and the test runs in zero real time.
func TestShutdownDrainsConfigSiphon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		shutdown func(runner *Runner, cancel context.CancelFunc)
	}{
		{
			name: "ctx cancel",
			shutdown: func(_ *Runner, cancel context.CancelFunc) {
				cancel()
			},
		},
		{
			name: "Stop()",
			shutdown: func(r *Runner, _ context.CancelFunc) {
				r.Stop()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// Slow factory keeps the reader busy in createAndStartServer
				// long enough for subsequent senders to be parked on the
				// unbuffered siphon when shutdown is triggered. The sleep
				// is synthetic under synctest and advances automatically.
				factory := func(ctx context.Context, _ string, _ *httpserver.Config, _ slog.Handler) (httpServerRunner, error) {
					time.Sleep(50 * time.Millisecond)
					m := mocks.NewMockRunnableWithStateable()
					m.On("Run", mock.Anything).Run(func(mock.Arguments) {
						<-ctx.Done()
					}).Return(nil)
					m.On("Stop").Return().Maybe()
					m.On("GetState").Return(finitestate.StatusRunning)
					m.On("IsReady").Return(true)
					stateChan := make(chan string, 1)
					stateChan <- finitestate.StatusRunning
					m.On("GetStateChan", mock.Anything).Return(stateChan)
					return m, nil
				}

				runner, err := NewRunner(WithRunnerFactory(factory))
				require.NoError(t, err)

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				runErr := make(chan error, 1)
				go func() { runErr <- runner.Run(ctx) }()

				// Settle initialization so IsReady is observable.
				synctest.Wait()
				require.True(t, runner.IsReady())

				// Many senders each pushing a unique server config. The
				// reader is in the slow factory; all but one are parked
				// on the unbuffered siphon when shutdown begins.
				const numSenders = 20
				var senderWg sync.WaitGroup
				senderWg.Add(numSenders)
				siphon := runner.GetConfigSiphon()
				for i := range numSenders {
					go func() {
						defer senderWg.Done()
						addr := fmt.Sprintf(":%d", 18000+i)
						siphon <- map[string]*httpserver.Config{
							fmt.Sprintf("server%d", i): createTestHTTPConfig(t, addr),
						}
					}()
				}

				tt.shutdown(runner, cancel)

				// Block on Run's return. While the test goroutine is
				// durably blocked here, synctest advances time so the
				// reader's factory sleep completes, the shutdown path
				// runs, and the drain consumes the parked senders.
				// Run also blocks on drainDone before returning, so by
				// the time runErr fires the drain has already exited
				// — no lingering background goroutine for the bubble
				// cleanup to trip on.
				<-runErr

				// All senders must have completed. Without the drain
				// (bug), these stay parked and synctest reports a
				// deadlock when the bubble exits.
				senderWg.Wait()
			})
		})
	}
}

// TestDrainExitsWhenSiphonClosed verifies that the shutdown drain exits when
// the siphon channel is closed by its owner. WithCustomSiphonChannel lets
// callers own the channel; if a caller cancels the supervisor's context and
// also closes the siphon, the drain (spawned by the ctx-cancel case) would
// hot-loop on a receive that always returns immediately — pegging CPU and
// resetting the inactivity timer on every iteration until the hard deadline.
// The fix is to inspect the receive's ok value and return when the channel
// is closed.
//
// Under synctest, a spinning drain is detectable: it is never durably blocked
// (the receive returns immediately every iteration), so time cannot advance
// and the bubble cannot drain. synctest.Test reports the lingering goroutine
// when the test function returns.
func TestDrainExitsWhenSiphonClosed(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		customSiphon := make(chan map[string]*httpserver.Config)
		runner, err := NewRunner(WithCustomSiphonChannel(customSiphon))
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		runErr := make(chan error, 1)
		go func() { runErr <- runner.Run(ctx) }()
		synctest.Wait()
		require.True(t, runner.IsReady())

		// Cancel first so the reader picks runCtx.Done and spawns the
		// drain; settle so the drain is alive before the close arrives.
		cancel()
		synctest.Wait()

		// Now close the siphon. A drain that ignored ok would spin on
		// the closed receive forever.
		close(customSiphon)

		// Block on Run's return so synctest can advance time. With the
		// fix the drain observes !ok and exits; with the bug the drain
		// stays runnable and synctest will surface a deadlock when the
		// bubble cleanup runs.
		<-runErr
	})
}

// TestDrainExitsOnShutdownTimeout verifies the hard-deadline exit path: when
// a caller keeps publishing to the siphon faster than the drain's internal
// inactivity window, quiescence can never fire and the drain must rely on
// WithShutdownTimeout as its safety bound. Without that bound, a misbehaving
// publisher could keep the drain alive indefinitely and Run would block on
// drainDone forever.
//
// The test publishes every 50ms (well below the 100ms quiescence window) so
// the drain's inactivity timer is reset on every receive. The only exit path
// left is shutdownCtx.Done firing at the configured shutdownTimeout. After
// that, the drain exits, Run unblocks, runErr fires, and the publisher's
// next send parks forever — which the test asserts by observing that the
// publisher's success counter stops growing across additional synthetic time.
func TestDrainExitsOnShutdownTimeout(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		customSiphon := make(chan map[string]*httpserver.Config)
		runner, err := NewRunner(
			WithCustomSiphonChannel(customSiphon),
			WithShutdownTimeout(500*time.Millisecond),
		)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		runErr := make(chan error, 1)
		go func() { runErr <- runner.Run(ctx) }()
		synctest.Wait()
		require.True(t, runner.IsReady())

		// Cancel so Run enters its shutdown branch. After synctest.Wait
		// the drain has spawned, shutdown has completed (no servers),
		// and Run is blocked on drainDone — making the drain the only
		// consumer on the siphon.
		cancel()
		synctest.Wait()

		// Continuous publisher: 50ms interval < 100ms quiescence, so
		// the drain's inactivity timer is reset on every receive and
		// can never fire. Only shutdownTimeout can release the drain.
		stop := make(chan struct{})
		var sent atomic.Int64
		go func() {
			cfg := map[string]*httpserver.Config{
				"keepalive": createTestHTTPConfig(t, ":18999"),
			}
			for {
				select {
				case <-stop:
					return
				case customSiphon <- cfg:
					sent.Add(1)
					time.Sleep(50 * time.Millisecond)
				}
			}
		}()

		// Run will unblock when the drain exits — i.e. when the 500ms
		// shutdownTimeout fires. If the deadline didn't bound the drain,
		// the continuous publisher would keep it alive forever and this
		// receive would deadlock under synctest.
		<-runErr

		// Publisher's next send is now parked (no consumer). Sample
		// what it managed to deliver before the deadline fired.
		synctest.Wait()
		frozen := sent.Load()
		require.Positive(t, frozen,
			"publisher must have delivered values to the drain before the deadline")

		// More synthetic time: with the drain gone, the count cannot
		// change. If it does, the drain is still consuming and the
		// deadline did not bound it.
		time.Sleep(2 * time.Second)
		synctest.Wait()
		require.Equal(t, frozen, sent.Load(),
			"drain must exit on shutdownTimeout; publisher should be parked on send")

		// Release the publisher so the synctest bubble has no lingering
		// goroutines when it exits.
		close(stop)
	})
}
