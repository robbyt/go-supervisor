package supervisor

import (
	"context"
	"maps"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestStateDeduplication tests that the startStateMonitor implementation
// properly filters out duplicate state changes when it receives the same state
// multiple times in a row through the state channel.
func TestStateDeduplication(t *testing.T) {
	t.Parallel()

	// Create a context with a suitable timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create a channel for sending state updates
	stateChan := make(chan string, 10)
	runnable := mocks.NewMockRunnableWithStateable()
	runnable.On("String").Return("test-runnable")
	runnable.On("GetStateChan", mock.Anything).Return(stateChan)
	runnable.On("GetState").Return("initial")

	// Create a new supervisor with our test runnable
	pidZero, err := New(WithContext(ctx), WithRunnables(runnable))
	assert.NoError(t, err)

	// Track the broadcasts that occur
	broadcasts := []StateMap{}
	broadcastChan := make(chan StateMap, 10)
	unsubscribe := pidZero.AddStateSubscriber(broadcastChan)
	defer unsubscribe()

	// Collect broadcasts in a background goroutine
	collectDone := make(chan struct{})
	go func() {
		defer close(collectDone)
		for {
			select {
			case stateMap, ok := <-broadcastChan:
				if !ok {
					return
				}
				// Copy the map to avoid issues with concurrent modification
				copy := make(StateMap)
				maps.Copy(copy, stateMap)
				broadcasts = append(broadcasts, copy)
				t.Logf("Received broadcast: %+v", copy)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Store the initial state to match production behavior
	pidZero.stateMap.Store(runnable, "initial")

	// Start the state monitor
	pidZero.wg.Add(1)
	go pidZero.startStateMonitor()

	// Send the initial state to be discarded as per implementation
	t.Log("Sending 'initial' to be discarded")
	stateChan <- "initial"
	time.Sleep(50 * time.Millisecond)

	// Test sequence:
	// 1. Send "running" once - should trigger broadcast
	// 2. Send "running" twice more - should be ignored as duplicates
	// 3. Send "stopped" - should trigger broadcast
	// 4. Send "stopped" again - should be ignored as duplicate
	// 5. Send "error" - should trigger broadcast

	// First state change
	t.Log("Sending 'running' state")
	runnable.On("GetState").Return("running")
	stateChan <- "running"

	// Send duplicate states - should be ignored
	t.Log("Sending 'running' state again (should be ignored)")
	stateChan <- "running"

	t.Log("Sending 'running' state a third time (should be ignored)")
	stateChan <- "running"

	// Second state change
	t.Log("Sending 'stopped' state")
	runnable.On("GetState").Return("stopped")
	stateChan <- "stopped"

	// Another duplicate - should be ignored
	t.Log("Sending 'stopped' state again (should be ignored)")
	stateChan <- "stopped"

	// Third state change
	t.Log("Sending 'error' state")
	runnable.On("GetState").Return("error")
	stateChan <- "error"
	time.Sleep(100 * time.Millisecond)

	// Clean up and wait for collection to complete
	cancel()
	unsubscribe()
	close(broadcastChan)
	<-collectDone

	// Log final state for debugging
	t.Log("All broadcasts received:")
	for i, b := range broadcasts {
		t.Logf("  %d: %+v", i, b)
	}

	// Count number of each state broadcast received
	statesReceived := make(map[string]int)
	for _, broadcast := range broadcasts {
		// Look for the state of our test runnable
		if state, ok := broadcast[runnable.String()]; ok {
			statesReceived[state]++
		}
	}

	// Log state counts
	t.Logf("State broadcast counts: %+v", statesReceived)

	// We should have unique state broadcasts (one each)
	// for running, stopped, and error states
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
}
