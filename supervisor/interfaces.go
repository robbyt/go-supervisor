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
	"fmt"
)

// Runnable represents a service that can be run and stopped.
type Runnable interface {
	fmt.Stringer // Runnables implement a String() method to be identifiable in logs

	// Run starts the service with the given context and returns an error if it fails.
	// Run is a blocking call that runs the work unit until it is stopped.
	// Cleanup errors that occur during shutdown should be returned from Run so
	// the supervisor's own Run() returns the error and the failure becomes
	// observable to the supervisor's caller.
	Run(ctx context.Context) error
	// Stop signals the service to stop. Stop is a blocking call that returns
	// once the service has finished its teardown. By contract Stop returns no
	// error: failures during teardown should be surfaced via the runnable's
	// own logging or via Stateable.GetStateChan (e.g., transitioning to an
	// Error state); any error that should propagate to the supervisor must
	// come back through Run's return value.
	Stop()
}

// Reloadable represents a service that can be reloaded.
type Reloadable interface {
	// Reload signals the service to reload its configuration. Reload is a
	// blocking call that returns once the reload has completed (or aborted
	// via ctx). A non-nil return reports that the reload did not succeed.
	// Failures are surfaced via two parallel channels: this return value
	// (for callers that handle errors directly) and a Stateable transition
	// to an Error state (for state-channel observers). Implementations
	// should set the FSM to Error on internal failure AND return the
	// error so both consumers see the same outcome.
	Reload(ctx context.Context) error
}

// Stateable represents a service that reports its current state. It is
// orthogonal to Readiness: a service can be Stateable without being Readiness
// (no startup gate participation), and a service can be Readiness without
// being Stateable (transient readiness signal only). The supervisor uses
// Stateable for continuous state observation (logging, GetStateChan
// monitoring) and Readiness for the one-shot startup gate.
type Stateable interface {
	// GetState returns the current state of the service.
	GetState() string

	// GetStateChan returns a channel that will receive the current state of the service.
	GetStateChan(context.Context) <-chan string
}

// Readiness reports whether the service has finished its startup phase. The
// supervisor's Run() polls IsReady on every Readiness runnable before
// proceeding to spawn the next one, so a runnable only needs to implement
// Readiness if it has a meaningful startup phase the supervisor should wait
// on. Returning true means the service is initialized enough for downstream
// runnables to start; it does not imply the service is in any particular
// FSM state.
type Readiness interface {
	IsReady() bool
}

// ReloadSender represents a service that can trigger reloads.
type ReloadSender interface {
	// GetReloadTrigger returns a channel that emits signals when a reload is requested.
	GetReloadTrigger() <-chan struct{}
}

// ShutdownSender represents a service that can trigger system shutdown.
type ShutdownSender interface {
	// GetShutdownTrigger returns a channel that emits signals when a shutdown is requested.
	GetShutdownTrigger() <-chan struct{}
}
