package composite

import (
	"context"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// GetState returns the current state of the CompositeRunner.
func (r *Runner[T]) GetState() string {
	return r.fsm.GetState()
}

// GetStateChan returns a channel that will receive state updates.
func (r *Runner[T]) GetStateChan(ctx context.Context) <-chan string {
	return r.fsm.GetStateChanWithTimeout(ctx)
}

// GetStateChanWithTimeout returns a channel that emits state changes.
// It's a pass-through to the underlying finite state machine.
func (r *Runner[T]) GetStateChanWithTimeout(ctx context.Context) <-chan string {
	return r.fsm.GetStateChanWithTimeout(ctx)
}

// IsRunning returns true if the runner is in the Running state.
func (r *Runner[T]) IsRunning() bool {
	return r.fsm.GetState() == finitestate.StatusRunning
}

// setStateError marks the FSM as being in the error state.
func (r *Runner[T]) setStateError() {
	err := r.fsm.SetState(finitestate.StatusError)
	if err != nil {
		r.logger.Error("Failed to transition to Error state", "error", err)
	}
}

// GetChildStates returns a map of child runnable names to their states.
func (r *Runner[T]) GetChildStates() map[string]string {
	// Runnables lock not required, reading config and querying state
	// does not modify any internal state

	states := make(map[string]string)
	cfg := r.getConfig()
	if cfg == nil {
		return states
	}

	for _, entry := range cfg.Entries {
		if s, ok := any(entry.Runnable).(supervisor.Stateable); ok {
			states[entry.Runnable.String()] = s.GetState()
		} else {
			states[entry.Runnable.String()] = "unknown"
		}
	}

	return states
}
