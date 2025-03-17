package finiteState

import (
	"log/slog"

	"github.com/robbyt/go-fsm"
)

// New creates a new finite state machine with the specified logger using "standard" state transitions.
func New(handler slog.Handler) (*fsm.Machine, error) {
	return fsm.New(handler, StatusNew, TypicalTransitions)
}
