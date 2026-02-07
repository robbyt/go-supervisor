package finitestate

import (
	"context"
	"log/slog"
	"time"

	"github.com/robbyt/go-fsm/v2"
	"github.com/robbyt/go-fsm/v2/hooks"
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

// Machine wraps go-fsm v2 to provide a simplified API with broadcast support.
type Machine struct {
	*fsm.Machine
}

// GetStateChan returns a channel that emits the state whenever it changes.
// The current state is sent immediately. The channel is closed when the
// provided context is canceled.
func (s *Machine) GetStateChan(ctx context.Context) <-chan string {
	sourceCh := make(chan string, 1)
	if err := s.Machine.GetStateChan(ctx, sourceCh); err != nil {
		ch := make(chan string)
		close(ch)
		return ch
	}

	// go-fsm sends the current state synchronously into sourceCh before
	// returning. Forward it into outCh now so callers can do non-blocking reads.
	outCh := make(chan string, 1)
	outCh <- <-sourceCh

	go func() {
		defer close(outCh)
		for {
			select {
			case state := <-sourceCh:
				outCh <- state
			case <-ctx.Done():
				// Drain any state already buffered in sourceCh before closing.
				select {
				case state := <-sourceCh:
					outCh <- state
				default:
				}
				return
			}
		}
	}()

	return outCh
}

// New creates a new finite state machine with the specified logger and transitions.
func New(handler slog.Handler, t *transitions.Config) (*Machine, error) {
	registry, err := hooks.NewRegistry(
		hooks.WithLogHandler(handler),
		hooks.WithTransitions(t),
	)
	if err != nil {
		return nil, err
	}

	f, err := fsm.New(
		StatusNew,
		t,
		fsm.WithLogHandler(handler),
		fsm.WithCallbackRegistry(registry),
		fsm.WithBroadcastTimeout(5*time.Second),
	)
	if err != nil {
		return nil, err
	}

	return &Machine{Machine: f}, nil
}
