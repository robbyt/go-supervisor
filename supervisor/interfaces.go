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
	fmt.Stringer // Runnables needs a String() method to be identifiable in logs

	// Run starts the service with the given context and returns an error if it fails.
	// Run must be a blocking call that runs the work unit until it is stopped.
	Run(ctx context.Context) error
	// Stop signals the service to stop.
	// Stop must be a blocking call that stops the work unit.
	Stop()
}

// Reloadable represents a service that can be reloaded.
type Reloadable interface {
	// Reload signals the service to reload its configuration.
	// Reload is a blocking call that reloads the configuration of the work unit.
	Reload()
}

// Stateable represents a service that can report its state.
type Stateable interface {
	// GetState returns the current state of the service.
	GetState() string

	// GetStateChan returns a channel that will receive the current state of the service.
	GetStateChan(context.Context) <-chan string
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
