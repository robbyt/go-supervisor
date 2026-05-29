package httpcluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/require"
)

// TestShutdown_NoRaceWithConcurrentReaders is a regression test for a data
// race in shutdown(): it held only r.mu.RLock() while reassigning
// r.currentEntries, so it raced concurrent RLock readers such as
// GetServerCount() and String().
//
// Written with testing/synctest so the bubble bounds all spawned goroutines;
// the race detector still observes true concurrency inside the bubble. Run
// under `go test -race` — before the fix this fails with a write/read data
// race on r.currentEntries; after switching shutdown() to a write lock it
// passes clean.
func TestShutdown_NoRaceWithConcurrentReaders(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r, err := NewRunner()
		require.NoError(t, err)

		// Drive the FSM to Running so shutdown()'s Stopping/Stopped
		// transitions are valid without standing up the full Run loop.
		require.NoError(t, r.fsm.Transition(finitestate.StatusBooting))
		require.NoError(t, r.fsm.Transition(finitestate.StatusRunning))

		var stop atomic.Bool
		var wg sync.WaitGroup

		// Hammer the RLock readers concurrently with shutdown(). GetServerCount
		// is a pure r.mu.RLock + read of currentEntries, giving an unmasked
		// race signal against shutdown()'s reassignment of the same field.
		const readers = 16
		for i := 0; i < readers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for !stop.Load() {
					_ = r.GetServerCount()
					_ = r.String()
				}
			}()
		}

		// Empty cluster: shutdown still reassigns r.currentEntries (the
		// commit at the end of the function), which is the racy write.
		require.NoError(t, r.shutdown(context.Background()))

		stop.Store(true)
		wg.Wait()
	})
}
