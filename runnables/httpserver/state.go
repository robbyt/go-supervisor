package httpserver

import (
	"context"

	"github.com/robbyt/go-fsm"
)

// stateMachine defines the interface for the finite state machine that tracks
// the HTTP server's lifecycle states. This abstraction allows for different
// FSM implementations and simplifies testing.
type stateMachine interface {
	// Transition attempts to transition the state machine to the specified state.
	Transition(state string) error

	// TransitionBool attempts to transition the state machine to the specified state.
	TransitionBool(state string) bool

	// TransitionIfCurrentState attempts to transition the state machine to the specified state
	TransitionIfCurrentState(currentState, newState string) error

	// SetState sets the state of the state machine to the specified state.
	SetState(state string) error

	// GetState returns the current state of the state machine.
	GetState() string

	// GetStateChan returns a channel that emits the state machine's state whenever it changes.
	// The channel is closed when the provided context is canceled.
	GetStateChan(ctx context.Context) <-chan string
}

// setStateError transitions the state machine to the Error state,
// falling back to alternative approaches if the transition fails.
func (r *Runner) setStateError() {
	// First try with normal transition
	if r.fsm.TransitionBool(fsm.StatusError) {
		return
	}

	// If that fails, force the state using SetState
	r.logger.Debug("Using SetState to force Error state")
	if err := r.fsm.SetState(fsm.StatusError); err != nil {
		r.logger.Error("Failed to set Error state", "error", err)

		// Last resort - try to set to Unknown
		if err := r.fsm.SetState(fsm.StatusUnknown); err != nil {
			r.logger.Error("Failed to set Unknown state", "error", err)
		}
	}
}

// GetState returns the status of the HTTP server
func (r *Runner) GetState() string {
	return r.fsm.GetState()
}

// GetStateChan returns a channel that emits the HTTP server's state whenever it changes.
// The channel is closed when the provided context is canceled.
func (r *Runner) GetStateChan(ctx context.Context) <-chan string {
	return r.fsm.GetStateChan(ctx)
}
