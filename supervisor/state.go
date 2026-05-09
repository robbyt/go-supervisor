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
	"sync"
	"time"
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
		r, ok := key.(Runnable)
		if !ok {
			p.logger.Warn("stateMap key is not a Runnable; skipping", "key", key)
			return true
		}
		state, ok := value.(string)
		if !ok {
			p.logger.Warn("stateMap value is not a string; skipping", "runnable", r, "value", value)
			return true
		}
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
	if len(stateMap) == 0 {
		p.logger.Debug("No state to broadcast; stateMap is empty")
		return
	}

	p.stateSubscribers.Range(func(key, value any) bool {
		ch, ok := key.(chan StateMap)
		if !ok {
			p.logger.Warn("stateSubscribers key is not a chan StateMap; skipping", "key", key)
			return true
		}
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
// The many purposes of the stateMap:
//   - Provides a thread-safe cache of the latest state for each runnable
//   - Enables state change deduplication (avoiding duplicate broadcasts of identical states)
//   - Represents the supervisor's "truth" for the state of current runnables
//   - Allows state querying without directly accessing runnables (e.g. for APIs or UIs)
//
// State deduplication works by comparing incoming states against the previously recorded state.
// Only when a state differs from the previous one is it stored and broadcast, preventing
// unnecessary broadcasts and reducing system load when runnables emit frequent duplicate states.
//
// Shutdown: when the supervisor context is canceled, startStateMonitor waits up to
// p.stateMonitorShutdownTimeout (default 5s, configurable via
// WithStateMonitorShutdownTimeout) for its per-runnable goroutines to exit. Healthy
// goroutines exit promptly because their inner select listens on ctx.Done(); the bound
// is a safety net that prevents a misbehaving Stateable from blocking supervisor
// shutdown indefinitely. Hitting the bound logs a Warn and returns. A bound of 0
// disables the deadline (waits indefinitely).
func (p *PIDZero) startStateMonitor() {
	p.logger.Debug("Starting state monitor...")

	var wg sync.WaitGroup
	for _, run := range p.runnables {
		if stateable, ok := run.(Stateable); ok {
			wg.Add(1)
			go p.monitorStateable(run, stateable, &wg)
		}
	}

	<-p.ctx.Done()
	p.logger.Debug("State monitor received context done signal, waiting for monitors to exit...")
	p.boundedWaitOnStateGoroutines(&wg)
}

// monitorStateable consumes state updates from a single Stateable runnable
// and forwards changes to stateMap and subscribers. Identical consecutive
// states are deduplicated. Exits when the supervisor context is canceled
// or the state channel closes.
func (p *PIDZero) monitorStateable(r Runnable, s Stateable, wg *sync.WaitGroup) {
	defer wg.Done()

	stateChan := s.GetStateChan(p.ctx)
	if !p.discardInitialState(r, stateChan) {
		return
	}

	lastState := p.loadCachedState(r)
	for {
		select {
		case <-p.ctx.Done():
			return
		case state, ok := <-stateChan:
			if !ok {
				return
			}
			if state == lastState {
				p.logger.Debug(
					"Received duplicate state (ignoring)",
					"runnable", r,
					"state", state)
				continue
			}
			p.recordStateChange(r, state)
			lastState = state
			p.broadcastState()
		}
	}
}

// discardInitialState reads and drops the first value from stateChan. The
// initial state is already seeded in stateMap by startRunnable, so re-broadcasting
// it would produce a duplicate. Returns false if the supervisor context was
// canceled or the channel closed before the first state arrived.
func (p *PIDZero) discardInitialState(r Runnable, stateChan <-chan string) bool {
	select {
	case <-p.ctx.Done():
		return false
	case state, ok := <-stateChan:
		if !ok {
			return false
		}
		p.logger.Debug("Discarded initial state", "runnable", r, "state", state)
		return true
	}
}

// loadCachedState returns the cached state for r from stateMap. Returns ""
// if no entry exists or the stored value is the wrong type. The entry is
// expected to have been seeded by startRunnable.
func (p *PIDZero) loadCachedState(r Runnable) string {
	cur, ok := p.stateMap.Load(r)
	if !ok {
		return ""
	}
	s, ok := cur.(string)
	if !ok {
		return ""
	}
	return s
}

// recordStateChange swaps the runnable's state into stateMap and logs the
// transition. Logs Warn if no prior entry existed (startRunnable should
// always have seeded one).
func (p *PIDZero) recordStateChange(r Runnable, state string) {
	prev, loaded := p.stateMap.Swap(r, state)
	if !loaded {
		p.logger.Warn(
			"Unexpected State map entry created",
			"runnable", r,
			"state", state)
		return
	}
	p.logger.Debug(
		"State map entry updated",
		"runnable", r,
		"oldState", prev,
		"state", state)
}

// boundedWaitOnStateGoroutines waits up to p.stateMonitorShutdownTimeout
// for wg to reach zero. Hitting the bound logs a Warn and returns; the
// leaked goroutines remain bound by the broader Go runtime lifetime. A
// timeout of 0 disables the deadline (waits indefinitely). Extracted from
// startStateMonitor for direct testing of the timer.C branch under
// testing/synctest.
func (p *PIDZero) boundedWaitOnStateGoroutines(wg *sync.WaitGroup) {
	if p.stateMonitorShutdownTimeout == 0 {
		wg.Wait()
		p.logger.Debug("State monitor complete.")
		return
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(p.stateMonitorShutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
		p.logger.Debug("State monitor complete.")
	case <-timer.C:
		p.logger.Warn(
			"State monitor shutdown deadline exceeded; leaking monitor goroutines",
			"timeout", p.stateMonitorShutdownTimeout,
		)
	}
}
