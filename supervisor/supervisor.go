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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultStartupTimeout = 2 * time.Minute       // Default per-Runnable max timeout for startup
	DefaultStartupInitial = 50 * time.Millisecond // Initial wait time for startup

	// DefaultShutdownTimeout is the TOTAL timeout for waiting on all supervisor goroutines
	// (including external runnables) to complete during shutdown, *after* Stop() has been called
	// on all of the runnables.
	DefaultShutdownTimeout = 2 * time.Minute
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

	startupTimeout  time.Duration
	startupInitial  time.Duration
	shutdownTimeout time.Duration

	logger *slog.Logger
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

// WithStartupTimeout sets the startup timeout for the PIDZero instance.
func WithStartupTimeout(timeout time.Duration) Option {
	return func(p *PIDZero) {
		if timeout > 0 {
			p.startupTimeout = timeout
		}
	}
}

// WithStartupInitial sets the initial startup delay for the PIDZero instance.
func WithStartupInitial(initial time.Duration) Option {
	return func(p *PIDZero) {
		if initial > 0 {
			p.startupInitial = initial
		}
	}
}

// WithShutdownTimeout sets the timeout for graceful shutdown of all runnables.
// If the timeout is reached before all runnables have completed their shutdown,
// the supervisor will log a warning but continue with the shutdown process.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(p *PIDZero) {
		if timeout > 0 {
			p.shutdownTimeout = timeout
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
		startupTimeout:   DefaultStartupTimeout,
		startupInitial:   DefaultStartupInitial,
		shutdownTimeout:  DefaultShutdownTimeout,
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

	// Start a single reload manager if any runnable is reloadable
	for _, r := range p.runnables {
		if _, ok := r.(Reloadable); ok {
			p.wg.Add(1)
			go p.startReloadManager()
			break
		}
	}

	// Start a single state monitor if any runnable reports state
	for _, r := range p.runnables {
		if _, ok := r.(Stateable); ok {
			p.wg.Add(1)
			go p.startStateMonitor()
			break
		}
	}

	// Start a single shutdown manager if any runnable can trigger shutdown
	for _, r := range p.runnables {
		if _, ok := r.(ShutdownSender); ok {
			p.wg.Add(1)
			go p.startShutdownManager()
			break
		}
	}

	// Start each service in sequence
	for _, r := range p.runnables {
		p.wg.Add(1)
		go p.startRunnable(r) // start this runnable in a separate goroutine

		// if this Runnable implements the Stateable block here until IsRunning()
		if stateable, ok := r.(Stateable); ok {
			err := p.blockUntilRunnableReady(stateable)
			if err != nil {
				p.logger.Error("Failed to start runnable", "runnable", r, "error", err)
				p.Shutdown()
				return err // exit Run loop
			}
		} else {
			p.logger.Debug("Runnable does not implement Stateable, continuing", "runnable", r)
		}
	}

	// Begin reaping process to monitor signals and errors
	return p.reap()
}

// blockUntilRunnableReady blocks until the runnable is in a running state.
// This requires implementing the Stateable interface so the runnable can be monitored.
func (p *PIDZero) blockUntilRunnableReady(r Stateable) error {
	startupCtx, cancel := context.WithTimeout(p.ctx, p.startupTimeout)
	defer cancel()

	timeout := p.startupInitial
	ticker := time.NewTicker(timeout)
	defer ticker.Stop()

	logger := p.logger.With("runnable", r)
	time.Sleep(timeout) // Initial delay before checking the runnable state

	for {
		if r.IsRunning() {
			logger.Debug("Runnable is running")
			return nil
		}

		logger.Debug(
			"Waiting for runnable to start",
			"retryIn",
			timeout,
			"deadline",
			p.startupTimeout,
		)
		select {
		case err := <-p.errorChan:
			// error received from `startRunnable`- put it back in the channel for reap() to process it later
			p.errorChan <- err
			return fmt.Errorf("runnable failed to start: %w", err)
		case <-startupCtx.Done():
			return fmt.Errorf("timeout waiting for runnable to start: %w", startupCtx.Err())
		case <-p.ctx.Done():
			logger.Debug("Context canceled, stopping runnables")
			return nil
		case <-ticker.C:
			// continue waiting, adding an exponential backoff
			if r.IsRunning() {
				logger.Debug("Runnable is running")
				return nil
			}
			timeout = timeout * 2
			ticker.Reset(timeout)
		}
	}
}

// Shutdown stops all runnables in reverse initialization order and attempts
// to wait for their goroutines to complete. Each runnable's Stop method will
// always be called regardless of timeouts. However, if shutdownTimeout is
// configured and exceeded, this function will return before all goroutines
// complete, potentially causing unclean termination if the program exits
// immediately afterward.
func (p *PIDZero) Shutdown() {
	p.shutdownOnce.Do(func() {
		shutdownStart := time.Now()
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

			runnableStart := time.Now()
			p.logger.Debug("Stopping", "runnable", r)
			r.Stop()
			stopDuration := time.Since(runnableStart)

			// Log the state after stopping if available
			if stateable, ok := r.(Stateable); ok {
				finalState := stateable.GetState()
				p.stateMap.Store(r, finalState)
				p.logger.Debug("Post-shutdown state", "runnable", r, "state", finalState)
			}

			p.logger.Debug("Runnable stopped", "runnable", r, "duration", stopDuration)
		}

		p.logger.Debug("Waiting for runnables to complete...")
		p.cancel() // cancel the context for any remaining goroutines

		// Set up a timeout for wait if configured
		if p.shutdownTimeout > 0 {
			// Create a channel to signal when wg.Wait() completes
			done := make(chan struct{})
			go func() {
				p.wg.Wait()
				close(done)
			}()

			// Wait for either completion or timeout
			select {
			case <-done:
				p.logger.Debug("All goroutines completed")
			case <-time.After(p.shutdownTimeout):
				p.logger.Warn("Shutdown timeout exceeded waiting for goroutines",
					"timeout", p.shutdownTimeout,
					"elapsed", time.Since(shutdownStart))
			}
		} else {
			// No timeout configured, just wait
			p.wg.Wait()
		}

		close(p.errorChan) // close the error channel, since no runnables can send errors

		totalShutdownTime := time.Since(shutdownStart)
		p.logger.Debug("Shutdown complete", "duration", totalShutdownTime)
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

	// Create a child context for this runnable to prevent cross-cancellation issues
	runCtx, cancel := context.WithCancel(p.ctx)
	defer cancel()

	// Run the runnable with the child context
	err := r.Run(runCtx)
	logger := p.logger.With("runnable", r)
	if err == nil {
		logger.Debug("Runnable completed without error")
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Filter out expected cancellation errors
		logger.Debug("Runnable stopped gracefully", "reason", err)
		return
	}

	// Unexpected error - log and send to the errorChan
	logger = logger.With("error", err)
	if stateable, ok := r.(Stateable); ok {
		logger.Error("Service failed", "state", stateable.GetState())
	} else {
		logger.Error("Service failed")
	}

	select {
	case p.errorChan <- fmt.Errorf("failed to start runnable: %w", err):
		// Error sent successfully
	default:
		logger.Warn("Unable to send error to errorChan (full or closed?)")
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
