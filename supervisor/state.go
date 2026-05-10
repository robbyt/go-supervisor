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

// GetStateMap returns each Stateable runnable's current state, keyed by the
// runnable's String() representation. The result is a best-effort, per-runnable
// live read: runnables are iterated sequentially and GetState() is called on
// each, so entries may reflect slightly different moments in time. There is
// no cached layer; each call queries the runnables directly.
func (p *PIDZero) GetStateMap() StateMap {
	out := make(StateMap)
	for _, r := range p.runnables {
		if s, ok := r.(Stateable); ok {
			out[r.String()] = s.GetState()
		}
	}
	return out
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

// broadcastState sends a fresh state snapshot to all subscribers. The snapshot
// is built before acquiring subscriberMutex so a slow GetState() implementation
// in one runnable doesn't stall subscribe/unsubscribe operations on other
// goroutines; the mutex is held only for the iteration over subscribers.
func (p *PIDZero) broadcastState() {
	stateMap := p.GetStateMap()
	if len(stateMap) == 0 {
		p.logger.Debug("No state to broadcast; no Stateable runnables")
		return
	}

	p.subscriberMutex.Lock()
	defer p.subscriberMutex.Unlock()

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

// startStateMonitor spawns one goroutine per Stateable runnable to forward
// state transitions to subscribers. Each goroutine subscribes to the runnable's
// state channel via GetStateChan, captures the initial state pushed on
// subscription as the dedup baseline (without broadcasting it — subscribers
// already receive the initial snapshot from AddStateSubscriber), and then
// broadcasts each subsequent transition that differs from the previous one.
//
// State queries (GetStateMap, GetCurrentState, GetCurrentStates) read live
// from each runnable's GetState(); the supervisor does not cache state. The
// monitor exists solely to push transitions to subscribers, not to maintain
// a state cache.
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
// and forwards transitions to subscribers. Identical consecutive states are
// deduplicated. Exits when the supervisor context is canceled or the state
// channel closes.
func (p *PIDZero) monitorStateable(r Runnable, s Stateable, wg *sync.WaitGroup) {
	defer wg.Done()

	stateChan := s.GetStateChan(p.ctx)
	lastState, ok := p.consumeInitialState(r, stateChan)
	if !ok {
		return
	}

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
			p.logger.Debug("State changed",
				"runnable", r,
				"oldState", lastState,
				"state", state)
			lastState = state
			p.broadcastState()
		}
	}
}

// consumeInitialState reads the first value pushed on stateChan after subscription.
// Stateable FSMs emit their current state on subscribe, so this captures the
// runnable's starting state to use as the dedup baseline. The value is NOT
// broadcast — subscribers receive the initial state via AddStateSubscriber's own
// snapshot send. Returns ("", false) if the supervisor context cancels or the
// channel closes before the first state arrives.
func (p *PIDZero) consumeInitialState(r Runnable, stateChan <-chan string) (string, bool) {
	select {
	case <-p.ctx.Done():
		return "", false
	case state, ok := <-stateChan:
		if !ok {
			return "", false
		}
		p.logger.Debug("Captured initial state", "runnable", r, "state", state)
		return state, true
	}
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
