package composite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// ConfigCallback is the function type signature for the callback used to load initial config, and new config during Reload()
type ConfigCallback[T runnable] func() (*Config[T], error)

// ReloadableWithConfig is an interface for sub-runnables that can reload with specific config
type ReloadableWithConfig interface {
	supervisor.Reloadable
	ReloadWithConfig(config any)
}

// CompositeRunner implements a component that manages multiple runnables of the same type
// as a single unit. It satisfies the Runnable, Reloadable, and Stateable interfaces.
type CompositeRunner[T runnable] struct {
	configMu       sync.RWMutex // Protects config access
	currentConfig  atomic.Pointer[Config[T]]
	configCallback ConfigCallback[T]

	runnablesMu  sync.Mutex // Protects runnable operations (start/stop)
	fsm          finitestate.Machine
	ctx          context.Context
	cancel       context.CancelFunc
	serverErrors chan error
	logger       *slog.Logger
}

// NewRunner creates a new CompositeRunner instance with the provided options.
func NewRunner[T runnable](opts ...Option[T]) (*CompositeRunner[T], error) {
	// Setup defaults
	logger := slog.Default().WithGroup("composite.Runner")
	ctx, cancel := context.WithCancel(context.Background())
	r := &CompositeRunner[T]{
		currentConfig: atomic.Pointer[Config[T]]{},
		serverErrors:  make(chan error, 1),
		ctx:           ctx,
		cancel:        cancel,
		logger:        logger,
	}

	// Apply options, to override defaults if provided
	for _, opt := range opts {
		opt(r)
	}

	// Validate requirements
	if r.configCallback == nil {
		return nil, fmt.Errorf(
			"%w: config callback is required (use WithConfigCallback)",
			ErrCompositeRunnable,
		)
	}

	// Create FSM
	machine, err := finitestate.New(
		r.logger.WithGroup("fsm").Handler(),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create fsm: %w", err)
	}
	r.fsm = machine

	return r, nil
}

// String returns a string representation of the CompositeRunner instance.
func (r *CompositeRunner[T]) String() string {
	cfg := r.getConfig()
	if cfg == nil {
		return "CompositeRunner<nil>"
	}
	return fmt.Sprintf("CompositeRunner{name: %s, entries: %d}", cfg.Name, len(cfg.Entries))
}

// Run starts all child runnables in order (first to last) and monitors for completion or errors.
// This method blocks until all child runnables are stopped or an error occurs.
func (r *CompositeRunner[T]) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Transition from New to Booting
	if err := r.fsm.Transition(finitestate.StatusBooting); err != nil {
		return fmt.Errorf("failed to transition to Booting state: %w", err)
	}

	// Start all child runnables
	if err := r.boot(); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to start child runnables: %w", err)
	}

	// Transition from Booting to Running
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to Running state: %w", err)
	}

	// Wait for context cancellation or errors
	select {
	case <-runCtx.Done():
		r.logger.Debug("Local context canceled")
	case <-r.ctx.Done():
		r.logger.Debug("Parent context canceled")
	case err := <-r.serverErrors:
		r.setStateError()
		return fmt.Errorf("%w: %w", ErrRunnableFailed, err)
	}

	// Try to transition to Stopping state
	if !r.fsm.TransitionBool(finitestate.StatusStopping) {
		if r.fsm.GetState() == finitestate.StatusStopping {
			r.logger.Debug("Already in Stopping state, continuing shutdown")
		} else {
			r.setStateError()
			return fmt.Errorf("failed to transition to Stopping state")
		}
	}

	// Stop all child runnables
	if err := r.stopRunnables(); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to stop runnables: %w", err)
	}

	// Transition to Stopped state
	if err := r.fsm.Transition(finitestate.StatusStopped); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to Stopped state: %w", err)
	}

	r.logger.Debug("All child runnables shut down gracefully")
	return nil
}

// Stop will cancel the parent context, causing all child runnables to stop.
func (r *CompositeRunner[T]) Stop() {
	// Only transition to Stopping if we're currently Running
	if err := r.fsm.TransitionIfCurrentState(finitestate.StatusRunning, finitestate.StatusStopping); err != nil {
		// This error is expected if we're already stopping, so only log at debug level
		r.logger.Debug("Not transitioning to Stopping state", "error", err)
	}
	r.cancel()
}

// boot starts all child runnables in the order they're defined.
func (r *CompositeRunner[T]) boot() error {
	r.runnablesMu.Lock()
	defer r.runnablesMu.Unlock()

	cfg := r.getConfig()
	if cfg == nil {
		return fmt.Errorf("%w: configuration is unavailable", ErrConfigMissing)
	}

	if len(cfg.Entries) == 0 {
		return fmt.Errorf("%w: no runnables to manage", ErrNoRunnables)
	}

	r.logger.Debug("Starting child runnables...", "count", len(cfg.Entries))

	// Use a temporary WaitGroup to ensure all goroutines are launched.
	var startWg sync.WaitGroup
	startWg.Add(len(cfg.Entries))

	for i, entry := range cfg.Entries {
		runnable := entry.Runnable
		index := i // Capture loop variable

		r.logger.Debug("Launching child runnable goroutine", "index", index, "runnable", runnable)

		// Start the runnable in its own goroutine
		go func(e T, idx int) {
			r.logger.Debug("Executing Run() for child runnable", "index", idx, "runnable", e)

			// Signal that the goroutine has started BEFORE calling Run
			// This allows boot() to return and the CompositeRunner to transition to Running
			startWg.Done()

			// Run the runnable; it blocks until stopped or error.
			// Use r.ctx which is the main context for the CompositeRunner lifetime.
			if err := e.Run(r.ctx); err != nil {
				// Filter out expected context cancellation errors on shutdown
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					r.logger.Error(
						"Child runnable returned error",
						"index",
						idx,
						"runnable",
						e,
						"error",
						err,
					)
					// Send non-cancellation errors to the main error channel
					// Use non-blocking send in case channel is full or Run exited
					select {
					case r.serverErrors <- fmt.Errorf("failed to start child runnable %d: %w", idx, err):
					default:
						r.logger.Warn(
							"Failed to send child runnable error to main channel (full or closed?)",
							"index",
							idx,
							"error",
							err,
						)
					}
				} else {
					r.logger.Debug("Child runnable stopped gracefully", "index", idx, "runnable", e, "reason", err)
				}
			} else {
				r.logger.Debug("Child runnable completed Run() without error", "index", idx, "runnable", e)
			}
		}(runnable, index)
	}

	// Wait for all goroutines to at least *start* their execution.
	// Note: This doesn't guarantee they are fully "ready".
	startWg.Wait()

	r.logger.Debug("All child runnable goroutines launched")
	// boot itself succeeds once goroutines are launched.
	// Runtime errors are handled asynchronously via r.serverErrors.
	return nil
}

// stopRunnables stops all child runnables in reverse order (last to first).
func (r *CompositeRunner[T]) stopRunnables() error {
	// Lock the runnables mutex to protect operations
	r.runnablesMu.Lock()
	defer r.runnablesMu.Unlock()

	cfg := r.getConfig()
	if cfg == nil {
		return ErrConfigMissing
	}

	// Create a WaitGroup to wait for all runnables to complete their Stop call
	var wg sync.WaitGroup
	wg.Add(len(cfg.Entries))

	// Stop each runnable in reverse order
	for i := len(cfg.Entries) - 1; i >= 0; i-- {
		entry := cfg.Entries[i]
		r.logger.Debug("Stopping child runnable", "index", i, "runnable", entry.Runnable)

		go func(run T) {
			run.Stop()
			wg.Done()
		}(entry.Runnable)
	}

	// Wait for all runnables to complete stopping
	wg.Wait()
	return nil
}

// setConfig atomically updates the current configuration.
// Caller must hold configMu write lock
func (r *CompositeRunner[T]) setConfig(config *Config[T]) {
	r.currentConfig.Store(config)
	r.logger.Debug("Config updated", "config", config)
}

// getConfig returns the current configuration, loading it via the callback if necessary.
func (r *CompositeRunner[T]) getConfig() *Config[T] {
	// First try to get config without locking
	config := r.currentConfig.Load()
	if config != nil {
		return config
	}

	// Need to load config, acquire write lock
	r.configMu.Lock()
	defer r.configMu.Unlock()

	// Check again after acquiring lock (double-checked locking pattern)
	if config := r.currentConfig.Load(); config != nil {
		return config
	}

	r.logger.Debug("Loading new config via callback")
	newConfig, err := r.configCallback()
	if err != nil {
		r.logger.Error("Failed to load config", "error", err)
		return nil
	}

	if newConfig == nil {
		r.logger.Error("Config callback returned nil")
		return nil
	}

	r.setConfig(newConfig)
	return newConfig
}
