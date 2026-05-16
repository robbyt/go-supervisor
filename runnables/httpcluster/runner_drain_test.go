package httpcluster

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
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
				<-runErr

				// All senders must have completed. Without the drain
				// (bug), these stay parked and synctest reports a
				// deadlock when the bubble exits.
				senderWg.Wait()

				// The drain does not observe ctx — it exits when its
				// inactivity timer (100ms synthetic) fires. Synthetic
				// sleep past it so the drain returns cleanly before the
				// bubble checks for lingering goroutines. (Real elapsed
				// time inside the bubble is zero.)
				time.Sleep(150 * time.Millisecond)
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
