package supervisor

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestStateDeduplication tests that the startStateMonitor implementation
// properly filters out duplicate state changes when it receives the same state
// multiple times in a row through the state channel.
func TestStateDeduplication(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		stateChan := make(chan string, 10)
		runnable := mocks.NewMockRunnableWithStateable()
		runnable.On("String").Return("test-runnable")
		runnable.On("GetStateChan", mock.Anything).Return(stateChan)
		runnable.On("GetState").Return("initial")

		pidZero, err := New(WithContext(ctx), WithRunnables(runnable))
		require.NoError(t, err)

		broadcastChan := make(chan StateMap, 10)
		unsubscribe := pidZero.AddStateSubscriber(broadcastChan)
		defer unsubscribe()

		statesReceived := make(map[string]int)
		collectDone := make(chan struct{})
		go func() {
			defer close(collectDone)
			for {
				select {
				case stateMap, ok := <-broadcastChan:
					if !ok {
						return
					}
					if state, ok := stateMap[runnable.String()]; ok {
						statesReceived[state]++
					}
					t.Logf("Received broadcast: %+v", stateMap)
				case <-ctx.Done():
					return
				}
			}
		}()

		pidZero.stateMap.Store(runnable, "initial")
		pidZero.wg.Go(pidZero.startStateMonitor)

		// Test sequence:
		// 1. Send "initial" - should be discarded (already captured in startRunnable)
		// 2. Send "running" once - should trigger broadcast
		// 3. Send "running" twice more - should be ignored as duplicates
		// 4. Send "stopped" - should trigger broadcast
		// 5. Send "stopped" again - should be ignored as duplicate
		// 6. Send "error" - should trigger broadcast

		t.Log("Sending 'initial' to be discarded")
		stateChan <- "initial"

		t.Log("Sending 'running' state")
		runnable.On("GetState").Return("running")
		stateChan <- "running"

		t.Log("Sending 'running' state again (should be ignored)")
		stateChan <- "running"

		t.Log("Sending 'running' state a third time (should be ignored)")
		stateChan <- "running"

		t.Log("Sending 'stopped' state")
		runnable.On("GetState").Return("stopped")
		stateChan <- "stopped"

		t.Log("Sending 'stopped' state again (should be ignored)")
		stateChan <- "stopped"

		t.Log("Sending 'error' state")
		runnable.On("GetState").Return("error")
		stateChan <- "error"

		synctest.Wait()

		cancel()
		unsubscribe()
		close(broadcastChan)
		<-collectDone

		t.Logf("State broadcast counts: %+v", statesReceived)

		assert.Equal(
			t, 1, statesReceived["running"],
			"Should receive exactly one 'running' state broadcast",
		)
		assert.Equal(
			t, 1, statesReceived["stopped"],
			"Should receive exactly one 'stopped' state broadcast",
		)
		assert.Equal(
			t, 1, statesReceived["error"],
			"Should receive exactly one 'error' state broadcast",
		)
	})
}
