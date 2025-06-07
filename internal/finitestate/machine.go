package finitestate

import (
	"context"
	"log/slog"
	"time"

	"github.com/robbyt/go-fsm"
)

const (
	StatusNew       = fsm.StatusNew
	StatusBooting   = fsm.StatusBooting
	StatusRunning   = fsm.StatusRunning
	StatusReloading = fsm.StatusReloading
	StatusStopping  = fsm.StatusStopping
	StatusStopped   = fsm.StatusStopped
	StatusError     = fsm.StatusError
	StatusUnknown   = fsm.StatusUnknown
)

// TypicalTransitions is a set of standard transitions for a finite state machine.
var TypicalTransitions = fsm.TypicalTransitions

// Machine is a wrapper around go-fsm.Machine that provides additional functionality.
type Machine struct {
	*fsm.Machine
}

// GetStateChan returns a channel that emits the state whenever it changes.
// The channel is closed when the provided context is canceled.
func (s *Machine) GetStateChanWithTimeout(ctx context.Context) <-chan string {
	return s.GetStateChanWithOptions(ctx, fsm.WithSyncTimeout(5*time.Second))
}

// New creates a new finite state machine with the specified logger using "standard" state transitions.
func New(handler slog.Handler) (*Machine, error) {
	f, err := fsm.New(handler, StatusNew, TypicalTransitions)
	if err != nil {
		return nil, err
	}
	return &Machine{Machine: f}, nil
}
