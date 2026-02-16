package finitestate

import (
	"context"
	"log/slog"
	"time"

	"github.com/robbyt/go-fsm/v2"
	"github.com/robbyt/go-fsm/v2/hooks"
	"github.com/robbyt/go-fsm/v2/hooks/broadcast"
	"github.com/robbyt/go-fsm/v2/transitions"
)

const (
	StatusNew       = transitions.StatusNew
	StatusBooting   = transitions.StatusBooting
	StatusRunning   = transitions.StatusRunning
	StatusReloading = transitions.StatusReloading
	StatusStopping  = transitions.StatusStopping
	StatusStopped   = transitions.StatusStopped
	StatusError     = transitions.StatusError
	StatusUnknown   = transitions.StatusUnknown
)

// typicalTransitions is a set of standard transitions for a finite state machine.
var typicalTransitions = transitions.Typical

// Machine wraps go-fsm v2 to provide a simplified API with broadcast support.
type Machine struct {
	*fsm.Machine
	broadcastManager *broadcast.Manager
}

// GetStateChan returns a channel that emits the state whenever it changes.
// The current state is sent immediately. The channel is closed when the
// provided context is canceled.
// A 5-second broadcast timeout prevents slow consumers from blocking state updates.
func (s *Machine) GetStateChan(ctx context.Context) <-chan string {
	return s.getStateChanInternal(ctx, broadcast.WithTimeout(5*time.Second))
}

// getStateChanInternal subscribes to state changes via the broadcast manager
// and sends the current state immediately on the returned channel.
func (s *Machine) getStateChanInternal(ctx context.Context, opts ...broadcast.Option) <-chan string {
	wrappedCh := make(chan string, 1)

	userCh, err := s.broadcastManager.GetStateChan(ctx, opts...)
	if err != nil {
		close(wrappedCh)
		return wrappedCh
	}

	currentState := s.GetState()
	wrappedCh <- currentState

	go func() {
		defer close(wrappedCh)
		for state := range userCh {
			wrappedCh <- state
		}
	}()

	return wrappedCh
}

// newMachine creates a new finite state machine with the specified logger and transitions.
func newMachine(handler slog.Handler, t *transitions.Config) (*Machine, error) {
	registry, err := hooks.NewRegistry(
		hooks.WithLogHandler(handler),
		hooks.WithTransitions(t),
	)
	if err != nil {
		return nil, err
	}

	broadcastManager := broadcast.NewManager(handler)

	err = registry.RegisterPostTransitionHook(hooks.PostTransitionHookConfig{
		Name:   "broadcast",
		From:   []string{"*"},
		To:     []string{"*"},
		Action: broadcastManager.BroadcastHook,
	})
	if err != nil {
		return nil, err
	}

	f, err := fsm.New(
		StatusNew,
		t,
		fsm.WithLogHandler(handler),
		fsm.WithCallbackRegistry(registry),
	)
	if err != nil {
		return nil, err
	}

	return &Machine{
		Machine:          f,
		broadcastManager: broadcastManager,
	}, nil
}

// NewTypicalFSM creates a new finite state machine with standard transitions.
func NewTypicalFSM(handler slog.Handler) (*Machine, error) {
	return newMachine(handler, typicalTransitions)
}
