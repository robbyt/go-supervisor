package httpcluster

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/mocks"
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
// or the drain consumes the value.
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
			t.Parallel()

			// Slow factory keeps the reader busy in createAndStartServer
			// long enough for subsequent senders to be parked on the
			// unbuffered siphon when shutdown is triggered.
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
			require.Eventually(t, runner.IsReady, time.Second, 10*time.Millisecond)

			// Many senders each pushing a unique server config, so the reader
			// has real work (factory call) per receive and the senders pile up.
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

			// Give the reader time to consume one or two configs and start
			// the slow factory, parking the remaining senders.
			time.Sleep(30 * time.Millisecond)

			tt.shutdown(runner, cancel)

			select {
			case <-runErr:
			case <-time.After(5 * time.Second):
				t.Fatal("Run() did not return after shutdown")
			}

			done := make(chan struct{})
			go func() {
				senderWg.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Siphon senders blocked after shutdown; drain did not unblock them")
			}
		})
	}
}
