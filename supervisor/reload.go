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

import "sync"

// ReloadAll triggers a reload of all runnables that implement the Reloadable interface.
// This call blocks until the reload manager accepts the signal. Callers that need
// non-blocking behavior should invoke this in a goroutine.
func (p *PIDZero) ReloadAll() {
	p.reloadListener <- struct{}{}
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

// reloadAllRunnables calls the Reload method on all runnables that implement the Reloadable
// interface.
func (p *PIDZero) reloadAllRunnables() int {
	reloads := 0
	p.logger.Info("Starting Reload...")

	for _, r := range p.runnables {
		if reloader, ok := r.(Reloadable); ok {
			// Log pre-reload state if available
			if stateable, ok := r.(Stateable); ok {
				preState := stateable.GetState()
				p.logger.Debug("Pre-reload state", "runnable", r, "state", preState)
			}

			p.logger.Debug("Reloading", "runnable", r)
			reloader.Reload()
			reloads++

			if stateable, ok := r.(Stateable); ok {
				postState := stateable.GetState()
				p.stateMap.Store(r, postState)
				p.logger.Debug("Post-reload state", "runnable", r, "state", postState)
			}

			continue
		}
		p.logger.Debug("Skipping Reload, not supported", "runnable", r)
	}
	return reloads
}
