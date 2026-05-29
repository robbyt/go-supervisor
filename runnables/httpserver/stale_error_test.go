package httpserver

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWaitForEvent_IgnoresStaleInstanceError is a regression test for a bug
// where a late error from a superseded server instance (the old server after
// a reload) would crash the healthy current runner. The fix makes each
// serverInstance own its error channel, so waitForEvent — which listens only
// on the current instance's channel — never observes a superseded server's
// error.
//
// Written with testing/synctest: synctest.Wait() returns once waitForEvent has
// parked in its select on the current instance's channel, making the "did it
// crash?" assertion deterministic without sleeps.
func TestWaitForEvent_IgnoresStaleInstanceError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := createReloadTestConfig(t, ":0", "/", time.Second)
		server, err := NewRunner(WithConfig(cfg))
		require.NoError(t, err)

		// Drive the FSM to Running so a fatal error would be observable as a
		// transition to Error.
		require.NoError(t, server.fsm.Transition(finitestate.StatusBooting))
		require.NoError(t, server.fsm.Transition(finitestate.StatusRunning))

		// The live instance, plus a superseded one from a "previous boot".
		curInst := &serverInstance{errCh: make(chan error, 1)}
		server.instance.Store(curInst)
		staleInst := &serverInstance{errCh: make(chan error, 1)}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		eventErr := make(chan error, 1)
		go func() { eventErr <- server.waitForEvent(ctx) }()

		// Late error from the superseded instance, on its own channel.
		staleInst.errCh <- assert.AnError

		// Let waitForEvent reach steady state (parked on curInst.errCh).
		synctest.Wait()

		// The stale error must NOT have crashed the runner.
		assert.Equal(t, finitestate.StatusRunning, server.GetState(),
			"stale instance error should be ignored, runner should stay Running")

		// Clean shutdown trigger: waitForEvent returns nil on ctx cancel.
		cancel()
		require.NoError(t, <-eventErr)
	})
}
