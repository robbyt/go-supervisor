package httpserver

import (
	"context"

	"github.com/robbyt/go-supervisor/internal/finiteState"
)

// setStateError transitions the state machine to the Error state,
// falling back to alternative approaches if the transition fails.
func (r *Runner) setStateError() {
	// First try with normal transition
	if r.fsm.TransitionBool(finiteState.StatusError) {
		return
	}

	// If that fails, force the state using SetState
	r.logger.Debug("Using SetState to force Error state")
	if err := r.fsm.SetState(finiteState.StatusError); err != nil {
		r.logger.Error("Failed to set Error state", "error", err)

		// Last resort - try to set to Unknown
		if err := r.fsm.SetState(finiteState.StatusUnknown); err != nil {
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
