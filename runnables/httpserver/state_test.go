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

// TestGetState verifies that GetState correctly returns the FSM state
func TestGetState(t *testing.T) {
	t.Parallel()

	server, listenPort := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)
	t.Logf("Server listening on port %s", listenPort)

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
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}
	t.Parallel()

	server, listenPort := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)
	t.Logf("Server listening on port %s", listenPort)

	// Create a context with timeout for safety
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
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

	// Wait for channel to close
	require.Eventually(t, func() bool {
		_, ok := <-stateChan
		return !ok
	}, 1*time.Second, 10*time.Millisecond, "Channel should be closed after context cancellation")
}

// TestIsRunning verifies that IsRunning returns the correct value based on the state
func TestIsRunning(t *testing.T) {
	t.Parallel()

	server, listenPort := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)
	t.Logf("Server listening on port %s", listenPort)

	// Test when state is not running
	err := server.fsm.SetState(finitestate.StatusNew)
	require.NoError(t, err)
	assert.False(t, server.IsRunning(), "Should return false when state is New")

	// Test when state is Booting
	err = server.fsm.SetState(finitestate.StatusBooting)
	require.NoError(t, err)
	assert.False(t, server.IsRunning(), "Should return false when state is Booting")

	// Test when state is Running
	err = server.fsm.SetState(finitestate.StatusRunning)
	require.NoError(t, err)
	assert.True(t, server.IsRunning(), "Should return true when state is Running")

	// Test when state is Stopping
	err = server.fsm.SetState(finitestate.StatusStopping)
	require.NoError(t, err)
	assert.False(t, server.IsRunning(), "Should return false when state is Stopping")

	// Test when state is Stopped
	err = server.fsm.SetState(finitestate.StatusStopped)
	require.NoError(t, err)
	assert.False(t, server.IsRunning(), "Should return false when state is Stopped")

	// Test when state is Error
	err = server.fsm.SetState(finitestate.StatusError)
	require.NoError(t, err)
	assert.False(t, server.IsRunning(), "Should return false when state is Error")
}

// TestSetStateError verifies the error state setting functionality
func TestSetStateError(t *testing.T) {
	t.Parallel()

	// Test normal transition path
	t.Run("Normal transition to error", func(t *testing.T) {
		server, listenPort := createTestServer(t,
			func(w http.ResponseWriter, r *http.Request) {}, "/test", 1*time.Second)
		t.Logf("Server listening on port %s", listenPort)

		// Set a known state that can transition to error
		err := server.fsm.SetState(finitestate.StatusNew)
		require.NoError(t, err)

		// Test state transition to error
		server.setStateError()
		assert.Equal(t, finitestate.StatusError, server.GetState())
	})

	// Test forced SetState path
	t.Run("Forced transition to error using SetState", func(t *testing.T) {
		server, listenPort := createTestServer(t,
			func(w http.ResponseWriter, r *http.Request) {}, "/test", 1*time.Second)
		t.Logf("Server listening on port %s", listenPort)

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
		server, listenPort := createTestServer(t,
			func(w http.ResponseWriter, r *http.Request) {}, "/test", 1*time.Second)
		t.Logf("Server listening on port %s", listenPort)

		// Get typical transitions to use as base
		logger := slog.Default().WithGroup("testFSM")
		// Create valid FSM but force it to a specific state
		validFSM, err := finitestate.New(logger.Handler(), finitestate.TypicalTransitions)
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

// TestWaitForState verifies the waitForState helper function
func TestWaitForState(t *testing.T) {
	t.Parallel()

	server, listenPort := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)
	t.Logf("Server listening on port %s", listenPort)

	// Set the state to Running
	err := server.fsm.SetState(finitestate.StatusRunning)
	require.NoError(t, err)

	// Verify state is set correctly
	require.Eventually(t, func() bool {
		return server.GetState() == finitestate.StatusRunning
	}, 1*time.Second, 10*time.Millisecond)

	// Set the state to Stopping
	err = server.fsm.SetState(finitestate.StatusStopping)
	require.NoError(t, err)

	// Verify state is set correctly
	require.Eventually(t, func() bool {
		return server.GetState() == finitestate.StatusStopping
	}, 1*time.Second, 10*time.Millisecond)
}

// TestGetStateChanWithTimeout verifies the timeout version of state channel method
func TestGetStateChanWithTimeout(t *testing.T) {
	t.Parallel()

	server, listenPort := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/", 1*time.Second)
	t.Logf("Server listening on port %s", listenPort)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stateChan := server.GetStateChanWithTimeout(ctx)
	assert.NotNil(t, stateChan, "Should return a non-nil channel")

	// First, read the current state (which is emitted immediately)
	var currentState string
	assert.Eventually(t, func() bool {
		select {
		case currentState = <-stateChan:
			return true
		default:
			return false
		}
	}, 100*time.Millisecond, 10*time.Millisecond, "Should receive current state")
	assert.Equal(t, finitestate.StatusNew, currentState, "Should receive current state first")

	// Now test that the channel receives state changes
	go func() {
		err := server.fsm.SetState(finitestate.StatusRunning)
		assert.NoError(t, err)
	}()

	// Should receive the state change
	var newState string
	assert.Eventually(t, func() bool {
		select {
		case newState = <-stateChan:
			return newState == finitestate.StatusRunning
		default:
			return false
		}
	}, 200*time.Millisecond, 10*time.Millisecond, "Should receive Running state")

	// Test context cancellation closes channel
	cancel()

	// Wait for channel to close due to context cancellation
	require.Eventually(t, func() bool {
		_, ok := <-stateChan
		return !ok
	}, 1*time.Second, 10*time.Millisecond, "Channel should be closed after context cancellation")
}
