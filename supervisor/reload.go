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
	"errors"
	"sync"
)

// ReloadAll triggers a reload of all runnables that implement the Reloadable
// interface. Blocks until the reload manager accepts the signal OR the
// supervisor's context is done — once shutdown begins, startReloadManager
// returns and stops draining reloadListener, so the send must abort instead
// of wedging. Callers that need non-blocking behavior should invoke this in
// a goroutine.
func (p *PIDZero) ReloadAll() {
	select {
	case p.reloadListener <- struct{}{}:
	case <-p.ctx.Done():
	}
}

// startReloadManager starts a goroutine that listens for reload notifications
// and calls the reload method on all reloadable services. This will also prevent
// multiple reloads from happening concurrently.
func (p *PIDZero) startReloadManager() {
	p.logger.Debug("Starting reload manager...")

	var senderWg sync.WaitGroup

	for _, run := range p.runnables {
		if rldSender, ok := run.(ReloadSender); ok {
			senderWg.Go(func() {
				for {
					select {
					case <-p.ctx.Done():
						return
					case <-rldSender.GetReloadTrigger():
						select {
						case p.reloadListener <- struct{}{}:
							p.logger.Debug("Reload notifier received from runnable", "runnable", run)
						case <-p.ctx.Done():
							return
						}
					}
				}
			})
		}
	}

	for {
		select {
		case <-p.ctx.Done():
			p.logger.Debug("Reload manager shutting down, waiting for sender listeners...")
			senderWg.Wait()
			p.logger.Debug("All sender listeners exited")
			return
		case <-p.reloadListener:
			reloads := p.reloadAllRunnables()
			p.logger.Info("Reload complete.", "runnablesReloaded", reloads)
		}
	}
}

// reloadAllRunnables calls the Reload method on all runnables that implement
// the Reloadable interface and returns the count of successful reloads.
func (p *PIDZero) reloadAllRunnables() int {
	p.logger.Info("Starting Reload...")
	reloads := 0
	for _, r := range p.runnables {
		reloader, ok := r.(Reloadable)
		if !ok {
			p.logger.Debug("Skipping Reload, not supported", "runnable", r)
			continue
		}
		if p.reloadOne(r, reloader) {
			reloads++
		}
	}
	return reloads
}

// reloadOne reloads a single runnable, brackets the call with pre/post state
// logging when the runnable is also Stateable, and returns whether the reload
// succeeded. Failures are best-effort: logged unless the error is just a
// context cancellation, never propagated. The runnable typically transitions
// its own FSM to Error so state-channel observers see the failure too.
func (p *PIDZero) reloadOne(r Runnable, reloader Reloadable) bool {
	p.logStateIfStateable(r, "Pre-reload state")
	defer p.logStateIfStateable(r, "Post-reload state")

	p.logger.Debug("Reloading", "runnable", r)
	if err := reloader.Reload(p.ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			p.logger.Error("Reload failed", "runnable", r, "error", err)
		}
		return false
	}
	return true
}

// logStateIfStateable emits a debug log of the runnable's current state if it
// implements Stateable; no-op otherwise.
func (p *PIDZero) logStateIfStateable(r Runnable, msg string) {
	if stateable, ok := r.(Stateable); ok {
		p.logger.Debug(msg, "runnable", r, "state", stateable.GetState())
	}
}
