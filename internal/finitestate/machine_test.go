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
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
