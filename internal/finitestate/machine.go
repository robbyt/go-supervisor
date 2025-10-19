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

// TypicalTransitions is a set of standard transitions for a finite state machine.
var TypicalTransitions = transitions.Typical

// Machine is a wrapper around go-fsm v2 that provides the v1 API compatibility.
// It manages both the FSM and broadcast functionality.
type Machine struct {
	*fsm.Machine
	broadcastManager *broadcast.Manager
}

// GetStateChanWithTimeout returns a channel that emits the state whenever it changes.
// The channel is closed when the provided context is canceled.
// For v1 API compatibility, the current state is sent immediately to the channel.
func (s *Machine) GetStateChanWithTimeout(ctx context.Context) <-chan string {
	return s.getStateChanInternal(ctx, broadcast.WithTimeout(5*time.Second))
}

// GetStateChan returns a channel that emits the state whenever it changes.
// The channel is closed when the provided context is canceled.
// For v1 API compatibility, the current state is sent immediately to the channel.
func (s *Machine) GetStateChan(ctx context.Context) <-chan string {
	return s.getStateChanInternal(ctx)
}

// GetStateChanWithOptions returns a channel that emits the state whenever it changes
// with custom broadcast options.
// For v1 API compatibility, the current state is sent immediately to the channel.
func (s *Machine) GetStateChanWithOptions(ctx context.Context, opts ...broadcast.Option) <-chan string {
	return s.getStateChanInternal(ctx, opts...)
}

// getStateChanInternal is a helper that creates a channel and sends the current state to it.
// This maintains v1 API compatibility where GetStateChan immediately sends the current state.
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

// New creates a new finite state machine with the specified logger using "standard" state transitions.
// This function provides compatibility with the v1 API while using v2 under the hood.
func New(handler slog.Handler) (*Machine, error) {
	registry, err := hooks.NewRegistry(
		hooks.WithLogHandler(handler),
		hooks.WithTransitions(TypicalTransitions),
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
		TypicalTransitions,
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
