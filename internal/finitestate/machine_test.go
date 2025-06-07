package finitestate

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/robbyt/go-fsm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("creates new machine with correct initial state", func(t *testing.T) {
		handler := slog.NewTextHandler(os.Stdout, nil)
		machine, err := New(handler)

		require.NoError(t, err)
		require.NotNil(t, machine)
		assert.Equal(t, StatusNew, machine.GetState())
	})

	t.Run("uses provided handler", func(t *testing.T) {
		// Create a test handler
		handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
		machine, err := New(handler)

		require.NoError(t, err)
		require.NotNil(t, machine)
	})
}

func TestMachineInterface(t *testing.T) {
	t.Parallel()

	setup := func() *Machine {
		handler := slog.NewTextHandler(os.Stdout, nil)
		m, err := New(handler)
		require.NoError(t, err)
		return m
	}

	t.Run("Transition changes state", func(t *testing.T) {
		machine := setup()

		// Initial state should be New
		assert.Equal(t, StatusNew, machine.GetState())

		// Transition to Booting
		err := machine.Transition(StatusBooting)
		require.NoError(t, err)
		assert.Equal(t, StatusBooting, machine.GetState())

		// Transition to Running
		err = machine.Transition(StatusRunning)
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, machine.GetState())
	})

	t.Run("Transition returns error for invalid transition", func(t *testing.T) {
		machine := setup()

		// Try invalid transition (New -> Running)
		err := machine.Transition(StatusRunning)
		require.Error(t, err)
		assert.Equal(
			t,
			StatusNew,
			machine.GetState(),
			"State shouldn't change on failed transition",
		)
	})

	t.Run("TransitionBool returns success status", func(t *testing.T) {
		machine := setup()

		// Valid transition
		success := machine.TransitionBool(StatusBooting)
		assert.True(t, success)
		assert.Equal(t, StatusBooting, machine.GetState())

		// Invalid transition
		success = machine.TransitionBool(StatusNew)
		assert.False(t, success)
		assert.Equal(
			t,
			StatusBooting,
			machine.GetState(),
			"State shouldn't change on failed transition",
		)
	})

	t.Run("TransitionIfCurrentState changes state when condition met", func(t *testing.T) {
		machine := setup()

		// Initial state is New
		err := machine.TransitionIfCurrentState(StatusNew, StatusBooting)
		require.NoError(t, err)
		assert.Equal(t, StatusBooting, machine.GetState())

		// Current state is Booting, should not change to New
		err = machine.TransitionIfCurrentState(StatusNew, StatusNew)
		require.Error(t, err)
		assert.Equal(t, StatusBooting, machine.GetState())

		// Current state is Booting, should change to Running
		err = machine.TransitionIfCurrentState(StatusBooting, StatusRunning)
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, machine.GetState())
	})

	t.Run("SetState forces state change", func(t *testing.T) {
		machine := setup()

		// Set state directly to Running (bypassing normal transitions)
		err := machine.SetState(StatusRunning)
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, machine.GetState())

		// Set state to Error
		err = machine.SetState(StatusError)
		require.NoError(t, err)
		assert.Equal(t, StatusError, machine.GetState())
	})

	t.Run("GetStateChan provides state updates", func(t *testing.T) {
		machine := setup()

		// First transition to booting
		err := machine.Transition(StatusBooting)
		require.NoError(t, err)
		assert.Equal(t, StatusBooting, machine.GetState())

		// Set up context with timeout
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// Set up the channel to receive state updates
		stateChan := machine.GetStateChan(ctx)
		require.NotNil(t, stateChan)

		// Drain any initial state notification that may be present
		select {
		case <-stateChan:
			// Ignore initial state
		case <-time.After(100 * time.Millisecond):
			// No initial state was sent, that's fine
		}

		// Transition to Running
		err = machine.Transition(StatusRunning)
		require.NoError(t, err)
		assert.Equal(t, StatusRunning, machine.GetState())

		// Wait for the state change notification
		var receivedState string
		select {
		case receivedState = <-stateChan:
			assert.Equal(t, StatusRunning, receivedState)
		case <-time.After(1 * time.Second):
			t.Fatal("Timeout waiting for Running state notification")
		}

		// Test that the channel closes when context is canceled
		cancel()

		// Wait for channel to close
		select {
		case _, open := <-stateChan:
			if open {
				t.Fatal("Channel should be closed after context cancellation")
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Timeout waiting for channel to close")
		}
	})
}

func TestTypicalTransitions(t *testing.T) {
	t.Parallel()

	t.Run("verify TypicalTransitions matches fsm package", func(t *testing.T) {
		assert.Equal(t, fsm.TypicalTransitions, TypicalTransitions)
	})

	t.Run("verify status constants match fsm package", func(t *testing.T) {
		assert.Equal(t, fsm.StatusNew, StatusNew)
		assert.Equal(t, fsm.StatusBooting, StatusBooting)
		assert.Equal(t, fsm.StatusRunning, StatusRunning)
		assert.Equal(t, fsm.StatusReloading, StatusReloading)
		assert.Equal(t, fsm.StatusStopping, StatusStopping)
		assert.Equal(t, fsm.StatusStopped, StatusStopped)
		assert.Equal(t, fsm.StatusError, StatusError)
		assert.Equal(t, fsm.StatusUnknown, StatusUnknown)
	})
}

func TestGetStateChanWithTimeout(t *testing.T) {
	t.Parallel()

	setup := func() *Machine {
		handler := slog.NewTextHandler(os.Stdout, nil)
		m, err := New(handler)
		require.NoError(t, err)
		return m
	}

	t.Run("provides state updates with timeout", func(t *testing.T) {
		machine := setup()

		// Set up context
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Get state channel with timeout
		stateChan := machine.GetStateChanWithTimeout(ctx)
		require.NotNil(t, stateChan)

		// Should receive initial state
		assert.Eventually(t, func() bool {
			select {
			case state := <-stateChan:
				return state == StatusNew
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Should receive initial state")

		// Transition to Booting and verify state update
		err := machine.Transition(StatusBooting)
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			select {
			case state := <-stateChan:
				return state == StatusBooting
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Should receive Booting state")

		// Transition to Running and verify state update
		err = machine.Transition(StatusRunning)
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			select {
			case state := <-stateChan:
				return state == StatusRunning
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Should receive Running state")

		// Cancel context and verify channel closes
		cancel()

		assert.Eventually(t, func() bool {
			_, open := <-stateChan
			return !open
		}, 1*time.Second, 10*time.Millisecond, "Channel should close when context is canceled")
	})

	t.Run("channel closes when context is canceled", func(t *testing.T) {
		machine := setup()

		// Set up context with timeout
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		// Get state channel with timeout
		stateChan := machine.GetStateChanWithTimeout(ctx)
		require.NotNil(t, stateChan)

		// Wait for context to timeout
		assert.Eventually(t, func() bool {
			_, open := <-stateChan
			return !open
		}, 200*time.Millisecond, 10*time.Millisecond, "Channel should close when context times out")
	})

	t.Run("multiple channels can be created", func(t *testing.T) {
		machine := setup()

		// Set up context
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Get multiple state channels
		stateChan1 := machine.GetStateChanWithTimeout(ctx)
		stateChan2 := machine.GetStateChanWithTimeout(ctx)
		require.NotNil(t, stateChan1)
		require.NotNil(t, stateChan2)
		assert.NotEqual(t, stateChan1, stateChan2, "Should create different channel instances")

		// Drain any initial states from both channels
		select {
		case <-stateChan1:
		default:
		}
		select {
		case <-stateChan2:
		default:
		}

		// Transition and verify both channels receive updates
		err := machine.Transition(StatusBooting)
		require.NoError(t, err)

		// Both channels should receive the state update
		assert.Eventually(t, func() bool {
			select {
			case state := <-stateChan1:
				return state == StatusBooting
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "First channel should receive state update")

		assert.Eventually(t, func() bool {
			select {
			case state := <-stateChan2:
				return state == StatusBooting
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Second channel should receive state update")
	})
}
