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

	// DefaultShutdownTimeout is the TOTAL wall-clock budget for graceful
	// shutdown, shared between per-runnable Stop() calls and the final wait
	// for goroutine completion. If a runnable's Stop() blocks past the
	// remaining budget, its goroutine is abandoned (logged) and shutdown
	// continues. A timeout of 0 disables the deadline.
	DefaultShutdownTimeout = 2 * time.Minute
)

// PIDZero manages multiple "runnables" and handles OS signals for HUP or graceful shutdown.
type PIDZero struct {
	ctx                context.Context
	runnables          []Runnable
	signalChan         chan os.Signal
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

// WithShutdownTimeout sets the TOTAL wall-clock timeout for graceful shutdown.
// The budget bounds both per-runnable Stop() calls and the final wait for
// goroutines to complete. If a runnable's Stop() blocks past the remaining
// budget, its goroutine is abandoned (logged warning) and shutdown
// continues to the next runnable. A timeout of 0 disables the deadline
// (waits indefinitely).
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
		signalChan:       make(chan os.Signal, 1), // OS signals must be buffered
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
			p.wg.Go(p.startReloadManager)
			break
		}
	}

	// Start a single state monitor if any runnable reports state
	for _, r := range p.runnables {
		if _, ok := r.(Stateable); ok {
			p.wg.Go(p.startStateMonitor)
			break
		}
	}

	// Start a single shutdown manager if any runnable can trigger shutdown
	for _, r := range p.runnables {
		if _, ok := r.(ShutdownSender); ok {
			p.wg.Go(p.startShutdownManager)
			break
		}
	}

	// Start each service in sequence
	for _, r := range p.runnables {
		p.wg.Go(func() {
			err := p.startRunnable(r)
			if err != nil {
				p.logger.Error("Runnable exited with error", "runnable", r, "error", err)
				p.errorChan <- err
			}
		})

		// if this Runnable implements the Stateable block here until IsRunning()
		if stateable, ok := r.(Stateable); ok {
			err := p.blockUntilRunnableReady(stateable)
			if err != nil {
				// Ctx-cancellation is a clean shutdown trigger, not a
				// runnable failure — match reap's nil return.
				if p.ctx.Err() != nil {
					p.logger.Debug("Context canceled while waiting for runnable", "runnable", r)
					p.Shutdown()
					return nil
				}
				p.logger.Error("Failed to start runnable", "runnable", r, "error", err)
				p.Shutdown()
				return err
			}
		} else {
			p.logger.Debug("Runnable does not implement Stateable, continuing", "runnable", r)
		}

		// Honor cancellation between iterations so a mid-startup cancel
		// (e.g. ShutdownSender firing, parent ctx) doesn't keep spawning
		// later runnables against an already-cancelled ctx.
		if p.ctx.Err() != nil {
			p.logger.Debug("Context canceled during startup")
			p.Shutdown()
			return nil
		}
	}

	// Begin reaping process to monitor signals and errors
	return p.reap()
}

// blockUntilRunnableReady blocks until the runnable is in a running state.
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
			return err
		case <-startupCtx.Done():
			return fmt.Errorf("timeout waiting for runnable to start: %w", startupCtx.Err())
		case <-p.ctx.Done():
			logger.Debug("Context canceled while waiting for runnable to start")
			return p.ctx.Err()
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

// Shutdown stops all runnables in reverse initialization order and waits for
// their goroutines to complete. The configured shutdown timeout is the TOTAL
// wall-clock budget for the entire shutdown, shared between per-runnable
// Stop() calls and the final wait for goroutines. If a runnable's Stop()
// blocks past the remaining budget, its goroutine is abandoned (logged
// warning) and shutdown continues. Abandoned runnable goroutines may keep
// running after Shutdown returns and will be reaped when the process exits.
// A timeout of 0 disables the deadline.
//
// The supervisor's context is cancelled before the Stop loop begins so that
// runnables which watch their runCtx for cancellation can begin teardown
// immediately, rather than waiting for the serial Stop() loop to reach them.
func (p *PIDZero) Shutdown() {
	p.shutdownOnce.Do(func() {
		shutdownStart := time.Now()
		p.logger.Info("Graceful shutdown has been initiated...")
		signal.Stop(p.signalChan) // stop listening for new signals

		// Cancel first so runnables watching runCtx can start teardown
		// before potentially-blocking Stop() calls reach them.
		p.cancel()

		// Compute the wall-clock deadline for the entire shutdown. Zero
		// means no deadline (wait indefinitely).
		var deadline time.Time
		if p.shutdownTimeout > 0 {
			deadline = shutdownStart.Add(p.shutdownTimeout)
		}

		// Stop each runnable in reverse order. Each Stop() runs in its own
		// goroutine so we can bound it by the remaining budget.
		for i := len(p.runnables) - 1; i >= 0; i-- {
			r := p.runnables[i]

			if stateable, ok := r.(Stateable); ok {
				p.logger.Debug("Pre-shutdown state",
					"runnable", r, "state", stateable.GetState())
			}

			runnableStart := time.Now()
			p.logger.Debug("Stopping", "runnable", r)
			stopped := p.stopRunnableBounded(r, deadline, shutdownStart)

			if stopped {
				if stateable, ok := r.(Stateable); ok {
					finalState := stateable.GetState()
					p.stateMap.Store(r, finalState)
					p.logger.Debug("Post-shutdown state",
						"runnable", r, "state", finalState)
				}
				p.logger.Debug("Runnable stopped",
					"runnable", r, "duration", time.Since(runnableStart))
			}
		}

		p.logger.Debug("Waiting for runnables to complete...")
		p.waitForGoroutines(deadline, shutdownStart)

		p.logger.Debug("Shutdown complete", "duration", time.Since(shutdownStart))
	})
}

// stopRunnableBounded calls r.Stop() in a goroutine and waits up to the
// remaining budget. Returns true if Stop() completed before the deadline,
// false if the goroutine was abandoned. A zero deadline means no deadline.
func (p *PIDZero) stopRunnableBounded(r Runnable, deadline, shutdownStart time.Time) bool {
	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()

	if deadline.IsZero() {
		<-stopDone
		return true
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		p.logger.Warn("Shutdown deadline already exceeded; abandoning Stop()",
			"runnable", r, "elapsed", time.Since(shutdownStart))
		return false
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-stopDone:
		return true
	case <-timer.C:
		p.logger.Warn("Stop() exceeded shutdown deadline; abandoning goroutine",
			"runnable", r,
			"elapsed", time.Since(shutdownStart),
			"timeout", p.shutdownTimeout)
		return false
	}
}

// waitForGoroutines waits for all runnable goroutines to complete, bounded
// by the remaining budget. A zero deadline waits indefinitely.
func (p *PIDZero) waitForGoroutines(deadline, shutdownStart time.Time) {
	if deadline.IsZero() {
		p.wg.Wait()
		return
	}

	remaining := time.Until(deadline)
	if remaining <= 0 {
		p.logger.Warn("Shutdown timeout exceeded; not waiting for goroutines",
			"timeout", p.shutdownTimeout,
			"elapsed", time.Since(shutdownStart))
		return
	}

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		p.logger.Debug("All goroutines completed")
	case <-timer.C:
		p.logger.Warn("Shutdown timeout exceeded waiting for goroutines",
			"timeout", p.shutdownTimeout,
			"elapsed", time.Since(shutdownStart))
	}
}

// SendSignal injects a signal into the supervisor's signal handling loop.
// This is useful for integration testing where OS signals cannot be sent directly,
// or for programmatically triggering signal-based behavior from application code.
// Calls made after shutdown are ignored.
func (p *PIDZero) SendSignal(sig os.Signal) {
	select {
	case p.signalChan <- sig:
	case <-p.ctx.Done():
		p.logger.Warn("SendSignal called after shutdown", "signal", sig)
	}
}

// listenForSignals starts listening for OS signals, sending them to the internal signalChan.
func (p *PIDZero) listenForSignals() {
	p.signalListenerOnce.Do(func() {
		p.logger.Debug("Listening for signals")
		signal.Notify(p.signalChan, p.subscribeSignals...)
	})
}

// startRunnable starts a service and sends any errors to the error channel
func (p *PIDZero) startRunnable(r Runnable) error {
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
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Filter out expected cancellation errors
			p.logger.Debug("Runnable stopped gracefully", "reason", err, "runnable", r)
			return nil
		}

		return err
	}
	return nil
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
		case sig := <-p.signalChan:
			switch sig {
			case syscall.SIGINT, syscall.SIGTERM:
				p.logger.Debug("Received signal", "signal", sig)
				p.Shutdown()
				return nil // exit reap loop
			case syscall.SIGHUP: // Reload runnables on SIGHUP
				p.logger.Debug("Received signal", "signal", sig)
				go p.ReloadAll()
				continue // keep on reaping!
			default:
				p.logger.Debug("Unhandled signal received", "signal", sig)
			}
		}
	}
}
