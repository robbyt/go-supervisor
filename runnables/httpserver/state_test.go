package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetStateError verifies the error state setting functionality
func TestSetStateError(t *testing.T) {
	t.Parallel()

	// Test normal transition path
	t.Run("Normal transition to error", func(t *testing.T) {
		server, _, _ := createTestServer(t,
			func(w http.ResponseWriter, r *http.Request) {}, "/test", 1*time.Second)

		// Set a known state that can transition to error
		err := server.fsm.SetState(finitestate.StatusNew)
		require.NoError(t, err)

		// Test state transition to error
		server.setStateError()
		assert.Equal(t, finitestate.StatusError, server.GetState())
	})

	// Test forced SetState path
	t.Run("Forced transition to error using SetState", func(t *testing.T) {
		server, _, _ := createTestServer(t,
			func(w http.ResponseWriter, r *http.Request) {}, "/test", 1*time.Second)

		// Set a state that won't normally transition to error
		// Force it to be in Running state first
		err := server.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// Then set a non-standard state
		err = server.fsm.Transition(finitestate.StatusStopping) // A valid transition
		require.NoError(t, err)

		// This should fail normal transition and use SetState instead
		server.setStateError()
		assert.Equal(t, finitestate.StatusError, server.GetState())
	})

	// Test transition to Error from Stopping
	t.Run("SetState to Error from Stopping state", func(t *testing.T) {
		server, _, _ := createTestServer(t,
			func(w http.ResponseWriter, r *http.Request) {}, "/test", 1*time.Second)

		// Get typical transitions to use as base
		logger := slog.Default().WithGroup("testFSM")
		// Create valid FSM but force it to a specific state
		validFSM, err := finitestate.New(logger.Handler())
		require.NoError(t, err)

		// Force it to be in Stopping state
		err = validFSM.SetState(finitestate.StatusStopping)
		require.NoError(t, err)

		// Save original FSM to restore after test
		originalFSM := server.fsm
		defer func() { server.fsm = originalFSM }()

		// Use the valid FSM for test
		server.fsm = validFSM

		// The state is already Stopping, which should require SetState to go to Error
		server.setStateError()

		// Should be able to transition to Error even from Stopping
		assert.Equal(t, finitestate.StatusError, server.GetState(), "Should be in Error state")
	})
}

// TestGetState verifies that GetState correctly returns the FSM state
func TestGetState(t *testing.T) {
	t.Parallel()

	server, _, _ := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)

	// Test initial state
	assert.Equal(t, finitestate.StatusNew, server.GetState(), "Initial state should be New")

	// Test after changing state
	err := server.fsm.SetState(finitestate.StatusRunning)
	require.NoError(t, err)
	assert.Equal(t, finitestate.StatusRunning, server.GetState(), "Should return Running state")

	// Test after another state change
	err = server.fsm.SetState(finitestate.StatusStopping)
	require.NoError(t, err)
	assert.Equal(t, finitestate.StatusStopping, server.GetState(), "Should return Stopping state")
}

// TestGetStateChan verifies that the state channel works correctly
func TestGetStateChan(t *testing.T) {
	t.Parallel()

	server, _, _ := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)

	// Create a context with timeout for safety
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Get the state channel
	stateChan := server.GetStateChan(ctx)

	// Verify we can receive state changes
	initialState := <-stateChan
	assert.Equal(t, finitestate.StatusNew, initialState, "Initial state should be New")

	// Make a state change
	err := server.fsm.SetState(finitestate.StatusRunning)
	require.NoError(t, err)

	// Verify the state change is received
	newState := <-stateChan
	assert.Equal(t, finitestate.StatusRunning, newState, "Should receive Running state")

	// Test context cancellation
	cancel()
	time.Sleep(100 * time.Millisecond) // Give time for channel to close

	// Channel should be closed
	_, ok := <-stateChan
	assert.False(t, ok, "Channel should be closed after context cancellation")
}

// waitForState waits for any of the expected states to be reached.
// It will keep reading from the channel until one of the expected states is seen
// or until the context is canceled.
func waitForState(t *testing.T, stateChan <-chan string, expectedStates []string) {
	t.Helper()

	// First check if the current state already matches
	currentState := ""
	select {
	case state := <-stateChan:
		currentState = state
	default:
		// No state available in channel yet
	}

	if currentState != "" {
		for _, expectedState := range expectedStates {
			if currentState == expectedState {
				return // Already in expected state
			}
		}
	}

	// Deadline to prevent test from hanging
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			// Before failing, check the object's current state directly
			objState := ""
			select {
			case objState = <-stateChan:
				// Got a state
			case <-time.After(100 * time.Millisecond):
				// Timeout getting state
			}

			t.Fatalf("Timed out waiting for states %v, current state: %s", expectedStates, objState)
		case state, ok := <-stateChan:
			if !ok {
				t.Fatalf("State channel closed while waiting for states %v", expectedStates)
			}

			for _, expectedState := range expectedStates {
				if state == expectedState {
					// Found expected state
					return
				}
			}
			// Continue waiting if state doesn't match any expected states
		}
	}
}

// waitForServerState waits for the server to reach the expected state
// nolint:unused
func waitForServerState(t *testing.T, server *Runner, expectedState string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stateChan := server.GetStateChan(ctx)
	waitForState(t, stateChan, []string{expectedState})
}

// TestWaitForState verifies the waitForState helper function
func TestWaitForState(t *testing.T) {
	t.Parallel()

	server, _, _ := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stateChan := server.GetStateChan(ctx)

	// Set the state after a short delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		err := server.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)
	}()

	// Wait for the state change
	waitForState(t, stateChan, []string{finitestate.StatusRunning})

	// Verify the current state
	assert.Equal(t, finitestate.StatusRunning, server.GetState())

	// Test waiting for multiple possible states
	go func() {
		time.Sleep(100 * time.Millisecond)
		err := server.fsm.SetState(finitestate.StatusStopping)
		require.NoError(t, err)
	}()

	// Should return when any of the expected states is reached
	waitForState(
		t,
		stateChan,
		[]string{finitestate.StatusStopped, finitestate.StatusStopping, finitestate.StatusError},
	)
	assert.Equal(t, finitestate.StatusStopping, server.GetState())
}
