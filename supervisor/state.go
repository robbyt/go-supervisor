/*
Copyright 2024 Robert Terhaar <robbyt@robbyt.net>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package supervisor

import (
	"context"
)

// StateMap is a map of runnable string representation to its current state
type StateMap map[string]string

// GetCurrentState returns the current state of a specific runnable service.
// If the service doesn't implement Stateable, returns "unknown".
func (p *PIDZero) GetCurrentState(r Runnable) string {
	if s, ok := r.(Stateable); ok {
		return s.GetState()
	}
	return "unknown"
}

// GetCurrentStates returns a map of all Stateable runnables and their current states.
func (p *PIDZero) GetCurrentStates() map[Runnable]string {
	states := make(map[Runnable]string)
	for _, r := range p.runnables {
		if s, ok := r.(Stateable); ok {
			states[r] = s.GetState()
		}
	}
	return states
}

// GetStateMap returns a map of runnable string representation to its current state.
// It uses cached state values from stateMap for consistency with the state monitoring system.
func (p *PIDZero) GetStateMap() StateMap {
	stateMap := make(StateMap)

	// Use the stateMap as the source of truth for consistent state reporting
	p.stateMap.Range(func(key, value any) bool {
		r := key.(Runnable)
		state := value.(string)
		stateMap[r.String()] = state
		return true
	})

	return stateMap
}

// AddStateSubscriber adds a channel to the internal list of broadcast targets. It will receive
// the current state immediately (if possible), and will also receive future state changes
// when any runnable's state is updated. A callback function is returned that should be called
// to remove the channel from the list of subscribers when it is no longer needed.
func (p *PIDZero) AddStateSubscriber(ch chan StateMap) func() {
	p.subscriberMutex.Lock()
	defer p.subscriberMutex.Unlock()

	p.stateSubscribers.Store(ch, struct{}{})

	// Try to send initial state
	select {
	case ch <- p.GetStateMap():
		p.logger.Debug("Sent initial state to subscriber")
	default:
		p.logger.Warn(
			"Unable to write initial state to channel; next state change will be sent instead",
		)
	}

	return func() {
		p.unsubscribeState(ch)
	}
}

// SubscribeStateChanges returns a channel that receives a StateMap whenever
// any runnable's state changes. The channel is closed when the context is done.
func (p *PIDZero) SubscribeStateChanges(ctx context.Context) <-chan StateMap {
	if ctx == nil {
		p.logger.Error("Context is nil; cannot create state channel")
		return nil
	}

	ch := make(chan StateMap, 10)
	unsubCallback := p.AddStateSubscriber(ch)

	go func() {
		// Block here until the context is done
		<-ctx.Done()

		// Unsubscribe and close the channel
		unsubCallback()
		close(ch)
	}()

	return ch
}

// unsubscribeState removes a channel from the internal list of broadcast targets.
func (p *PIDZero) unsubscribeState(ch chan StateMap) {
	p.subscriberMutex.Lock()
	defer p.subscriberMutex.Unlock()

	p.stateSubscribers.Delete(ch)
}

// broadcastState sends the current state map to all subscribers.
func (p *PIDZero) broadcastState() {
	// Lock the entire broadcast to prevent race conditions
	p.subscriberMutex.Lock()
	defer p.subscriberMutex.Unlock()

	stateMap := p.GetStateMap()
	if len(stateMap) == 0 || stateMap == nil {
		p.logger.Debug("No state to broadcast; stateMap is empty")
		return
	}

	p.stateSubscribers.Range(func(key, value any) bool {
		ch := key.(chan StateMap)
		select {
		case ch <- stateMap:
			p.logger.Debug("Sent state update to subscriber")
		default:
			p.logger.Warn("Subscriber channel is full; skipping broadcast")
		}
		return true // continue iteration
	})
}

// startStateMonitor initiates background goroutines to monitor state changes for each
// Stateable runnable. Each runnable that implements the Stateable interface will
// spawn a goroutine that will:
//
// 1. Obtains a state channel from the runnable via the GetStateChan() method
// 2. Discards the current/initial state from the channel (as it's already captured in startRunnable)
// 3. Begins monitoring for new states, deduplicating identical consecutive states
// 4. Updates the internal stateMap when states change
// 5. Broadcasts state changes to all subscribers
//
// The stateMap serves multiple critical functions:
//   - Provides a thread-safe cache of the latest state for each runnable
//   - Enables state change deduplication (avoiding duplicate broadcasts of identical states)
//   - Serves as the single source of truth for runnable states within the supervisor
//   - Allows state querying without directly accessing runnables (e.g. for APIs or UIs)
//
// State deduplication works by comparing incoming states against the previously recorded state.
// Only when a state differs from the previous one is it stored and broadcast, preventing
// unnecessary broadcasts and reducing system load when runnables emit frequent duplicate states.
func (p *PIDZero) startStateMonitor() {
	for _, r := range p.runnables {
		if stateable, ok := r.(Stateable); ok {
			r := r // capture loop variable
			go func() {
				stateChan := stateable.GetStateChan(p.ctx)

				// Read the first state and discard it - it's the initial state
				// that we've already captured and stored manually in startRunnable
				select {
				case state, ok := <-stateChan:
					if !ok {
						p.logger.Debug("State channel closed during initialization", "runnable", r)
						return
					}
					// First state discarded to avoid duplicate broadcast
					p.logger.Debug("Discarded initial state", "runnable", r, "state", state)
				case <-p.ctx.Done():
					return
				}

				// Keep track of the last state to avoid duplicate broadcasts
				var lastState string
				if currentState, loaded := p.stateMap.Load(r); loaded {
					lastState = currentState.(string)
				}

				for {
					select {
					case <-p.ctx.Done():
						return
					case state, ok := <-stateChan:
						if !ok {
							p.logger.Debug("State channel closed", "runnable", r)
							return
						}

						// Only broadcast if the state has actually changed from the last known state
						if state != lastState {
							p.stateMap.Store(r, state)
							p.logger.Debug(
								"State changed",
								"runnable",
								r,
								"oldState",
								lastState,
								"newState",
								state,
							)
							lastState = state

							// Broadcast state change to all subscribers
							p.broadcastState()
						} else {
							p.logger.Debug("Received duplicate state (ignoring)", "runnable", r, "state", state)
						}
					}
				}
			}()
		}
	}
}
