package httpcluster

import (
	"context"

	"github.com/robbyt/go-supervisor/internal/finitestate"
)

// GetState returns the current state of the cluster.
func (r *Runner) GetState() string {
	return r.fsm.GetState()
}

// GetStateChan returns a channel that receives state updates.
func (r *Runner) GetStateChan(ctx context.Context) <-chan string {
	return r.fsm.GetStateChan(ctx)
}

// IsRunning returns true if the cluster is in a running state.
func (r *Runner) IsRunning() bool {
	return r.fsm.GetState() == finitestate.StatusRunning
}

// setStateError transitions the state machine to the Error state.
func (r *Runner) setStateError() {
	if r.fsm.TransitionBool(finitestate.StatusError) {
		return
	}

	r.logger.Debug("Using SetState to force Error state")
	if err := r.fsm.SetState(finitestate.StatusError); err != nil {
		r.logger.Error("Failed to set Error state", "error", err)

		if err := r.fsm.SetState(finitestate.StatusUnknown); err != nil {
			r.logger.Error("Failed to set Unknown state", "error", err)
		}
	}
}
