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
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// PIDZero manages multiple "runnables" and handles OS signals for HUP or graceful shutdown.
type PIDZero struct {
	ctx                context.Context
	runnables          []Runnable
	SignalChan         chan os.Signal
	errorChan          chan error
	wg                 sync.WaitGroup
	cancel             context.CancelFunc
	signalListenerOnce sync.Once
	shutdownOnce       sync.Once
	subscribeSignals   []os.Signal
	reloadListener     chan struct{}
	stateMap           sync.Map
	stateSubscribers   sync.Map
	subscriberMutex    sync.Mutex
	logger             *slog.Logger
}

// Option represents a functional option for configuring PIDZero.
type Option func(*PIDZero)

// WithLogHandler sets a custom slog handler for the PIDZero instance.
// For example, to use a custom JSON handler with debug level:
//
//	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
//	sv := supervisor.New(runnables, supervisor.WithLogHandler(handler))
func WithLogHandler(handler slog.Handler) Option {
	return func(p *PIDZero) {
		if handler != nil {
			p.logger = slog.New(handler.WithGroup("Supervisor"))
		}
	}
}

// WithSignals sets custom signals for the PIDZero instance to listen for.
func WithSignals(signals ...os.Signal) Option {
	return func(p *PIDZero) {
		p.subscribeSignals = signals
	}
}

// WithContext sets a custom context for the PIDZero instance.
// This allows for more granular control over cancellation and timeouts.
func WithContext(ctx context.Context) Option {
	return func(p *PIDZero) {
		if ctx != nil {
			p.ctx, p.cancel = context.WithCancel(ctx)
		}
	}
}

// WithRunnables sets the runnables to be managed by the PIDZero instance.
// Accepts multiple runnables as variadic arguments.
func WithRunnables(runnables ...Runnable) Option {
	return func(p *PIDZero) {
		if len(runnables) > 0 {
			p.runnables = runnables
		}
	}
}

// New creates a new PIDZero instance with the provided options.
func New(opts ...Option) (*PIDZero, error) {
	// Load the default logger and append the Supervisor group
	logger := slog.Default().WithGroup("Supervisor")

	// Initialize with a background context by default
	ctx, cancel := context.WithCancel(context.Background())

	defaultSignals := []os.Signal{
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGHUP,
	}

	p := &PIDZero{
		runnables:        []Runnable{},
		SignalChan:       make(chan os.Signal, 1), // OS signals must be buffered
		errorChan:        make(chan error, 1),     // will be adjusted later
		ctx:              ctx,
		cancel:           cancel,
		subscribeSignals: defaultSignals,
		reloadListener:   make(chan struct{}),
		logger:           logger,
	}

	// Apply options
	for _, opt := range opts {
		opt(p)
	}

	if len(p.runnables) == 0 {
		return nil, fmt.Errorf("no runnables provided")
	}
	p.errorChan = make(chan error, len(p.runnables)*2)

	return p, nil
}

// String returns a string representation of the PIDZero instance.
func (p *PIDZero) String() string {
	return fmt.Sprintf("Supervisor<runnables: %d>", len(p.runnables))
}

// Run starts all runnables and listens for OS signals to handle graceful shutdown or reload.
func (p *PIDZero) Run() error {
	p.logger.Debug("Starting...")
	defer func() {
		p.logger.Info("Goodbye!")
	}()
	p.listenForSignals()

	p.wg.Add(1) // wait for reload manager upon exit
	go p.startReloadManager()

	// log state changes for all runnables that are "stateable"
	p.startStateMonitor()

	// Start each service in sequence
	for _, r := range p.runnables {
		go p.startRunnable(r)
		p.wg.Add(1) // wait for each runnable upon exit
	}

	// Begin reaping process
	return p.reap()
}

// Shutdown gracefully shuts down all runnables and cleans up resources.
func (p *PIDZero) Shutdown() {
	p.shutdownOnce.Do(func() {
		p.logger.Info("Graceful shutdown has been initiated...")
		signal.Stop(p.SignalChan) // stop listening for new signals

		// Stop each runnable in reverse order
		for i := len(p.runnables) - 1; i >= 0; i-- {
			r := p.runnables[i]

			// Log the state before stopping if available
			if stateable, ok := r.(Stateable); ok {
				currentState := stateable.GetState()
				p.logger.Debug("Pre-shutdown state", "runnable", r, "state", currentState)
			}

			p.logger.Debug("Stopping", "runnable", r)
			r.Stop()

			// Log the state after stopping if available
			if stateable, ok := r.(Stateable); ok {
				finalState := stateable.GetState()
				p.stateMap.Store(r, finalState)
				p.logger.Debug("Post-shutdown state", "runnable", r, "state", finalState)
			}
		}

		p.logger.Debug("Waiting for runnables to complete...")
		p.cancel()         // cancel the context, to close any other remaining goroutines
		p.wg.Wait()        // block here until all runnables have stopped
		close(p.errorChan) // close the error channel, since no runnables can send errors

		p.logger.Debug("Shutdown complete.")
	})
}

// listenForSignals starts listening for OS signals, sending them to the internal SignalChan.
func (p *PIDZero) listenForSignals() {
	p.signalListenerOnce.Do(func() {
		p.logger.Debug("Listening for signals")
		signal.Notify(p.SignalChan, p.subscribeSignals...)
	})
}

// startRunnable starts a service and sends any errors to the error channel
func (p *PIDZero) startRunnable(r Runnable) {
	defer p.wg.Done()

	// Log the initial state if available
	if stateable, ok := r.(Stateable); ok {
		initialState := stateable.GetState()
		p.stateMap.Store(r, initialState)
		p.logger.Debug("Initial state", "runnable", r, "state", initialState)
	}

	if err := r.Run(p.ctx); err != nil {
		p.logger.Error("Unable to start", "runnable", r, "error", err)
		if stateable, ok := r.(Stateable); ok {
			p.logger.Error("Service failed", "runnable", r, "state", stateable.GetState())
		}
		p.errorChan <- err
	}
}

// reap listens indefinitely for errors or OS signals and handles them appropriately.
func (p *PIDZero) reap() error {
	p.logger.Debug("Starting reap...")

	for {
		select {
		case err := <-p.errorChan:
			// A service returned an error; initiate shutdown
			p.logger.Debug("Error from runnable", "error", err)
			p.Shutdown()
			return err // exit reap loop and return error
		case <-p.ctx.Done():
			p.logger.Debug("Supervisor context canceled")
			p.Shutdown()
			return nil // exit reap loop
		case sig := <-p.SignalChan:
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				p.logger.Debug("Received signal", "signal", sig)
				p.Shutdown()
				return nil // exit reap loop
			case syscall.SIGHUP: // Reload runnables on SIGHUP
				p.logger.Debug("Received signal", "signal", sig)
				p.ReloadAll()
				continue // keep on reaping!
			default:
				p.logger.Debug("Unhandled signal received", "signal", sig)
			}
		}
	}
}
