package composite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/robbyt/go-supervisor/internal/finitestate"
)

// ConfigCallback is the function type signature for the callback used to load initial config, and new config during Reload()
type ConfigCallback[T runnable] func() (*Config[T], error)

// Runner implements a component that manages multiple runnables of the same type
// as a single unit. It satisfies the Runnable, Reloadable, and Stateable interfaces.
type Runner[T runnable] struct {
	configMu       sync.Mutex // Only used for getConfig()
	currentConfig  atomic.Pointer[Config[T]]
	configCallback ConfigCallback[T]

	runnablesMu sync.Mutex // Protects runnable operations (start/stop)
	fsm         finitestate.Machine

	runCtx       context.Context // set by Run() to track THIS instance run context
	parentCtx    context.Context // set by NewRunner() to track the parent context
	parentCancel context.CancelFunc
	serverErrors chan error
	logger       *slog.Logger
}

// NewRunner creates a new CompositeRunner instance with the provided configuration callback and options.
// Parameters:
//   - configCallback: Required. A function that returns the initial configuration and is called during any reload operations.
//   - opts: Optional. A variadic list of Option functions to customize the Runner behavior.
//
// The configCallback must not be nil and will be invoked by Run() to load the initial configuration.
func NewRunner[T runnable](
	configCallback ConfigCallback[T],
	opts ...Option[T],
) (*Runner[T], error) {
	// Setup defaults
	logger := slog.Default().WithGroup("composite.Runner")
	parentCtx, parentCancel := context.WithCancel(context.Background())
	r := &Runner[T]{
		currentConfig:  atomic.Pointer[Config[T]]{},
		configCallback: configCallback,

		runCtx:       context.Background(), // will be replaced in Run()
		parentCtx:    parentCtx,
		parentCancel: parentCancel,
		serverErrors: make(chan error, 1),
		logger:       logger,
	}

	// Apply options, to override defaults if provided
	for _, opt := range opts {
		opt(r)
	}

	// Validate requirements
	if r.configCallback == nil {
		return nil, fmt.Errorf(
			"%w: config callback is required",
			ErrCompositeRunnable,
		)
	}

	// Create FSM after the optional logger has been configured
	fsm, err := finitestate.New(
		r.logger.WithGroup("fsm").Handler(),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create fsm: %w", err)
	}
	r.fsm = fsm

	return r, nil
}

// String returns a string representation of the CompositeRunner instance.
func (r *Runner[T]) String() string {
	cfg := r.getConfig()
	if cfg == nil {
		return "CompositeRunner<nil>"
	}
	return fmt.Sprintf("CompositeRunner{name: %s, entries: %d}", cfg.Name, len(cfg.Entries))
}

// Run starts all child runnables in order (first to last) and monitors for completion or errors.
// This method blocks until all child runnables are stopped or an error occurs.
func (r *Runner[T]) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	r.runnablesMu.Lock()
	// store the Run context in the runner so that Reload() can use it later
	r.runCtx = runCtx
	r.runnablesMu.Unlock()

	// Transition from New to Booting
	if err := r.fsm.Transition(finitestate.StatusBooting); err != nil {
		return fmt.Errorf("failed to transition to Booting state: %w", err)
	}

	// Start all child runnables
	if err := r.boot(ctx); err != nil {
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
	case <-r.parentCtx.Done():
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
func (r *Runner[T]) Stop() {
	// Only transition to Stopping if we're currently Running
	if err := r.fsm.TransitionIfCurrentState(finitestate.StatusRunning, finitestate.StatusStopping); err != nil {
		// This error is expected if we're already stopping, so only log at debug level
		r.logger.Debug("Not transitioning to Stopping state", "error", err)
	}
	r.parentCancel()
}

// boot starts all child runnables in the order they're defined.
func (r *Runner[T]) boot(ctx context.Context) error {
	logger := r.logger.WithGroup("boot")
	r.runnablesMu.Lock()
	defer r.runnablesMu.Unlock()

	cfg := r.getConfig()
	if cfg == nil {
		return fmt.Errorf("%w: configuration is unavailable", ErrConfigMissing)
	}

	// If there are no entries, just log and return successfully instead of error
	if len(cfg.Entries) == 0 {
		logger.Debug("No runnables found in configuration")
		return nil
	}

	logger.Debug("Starting child runnables...", "count", len(cfg.Entries))

	// Use a temporary WaitGroup to ensure all goroutines are launched.
	var startWg sync.WaitGroup
	startWg.Add(len(cfg.Entries))
	for i, e := range cfg.Entries {
		go func(idx int, entry RunnableEntry[T]) {
			startWg.Done()
			r.startRunnable(ctx, entry.Runnable, idx)
		}(i, e)
	}
	startWg.Wait()
	logger.Debug("All child runnables launched")
	return nil
}

// startRunnable is a blocking call that starts a child runnable.
func (r *Runner[T]) startRunnable(ctx context.Context, subRunnable T, idx int) {
	logger := r.logger.WithGroup("child").With("index", idx, "runnable", subRunnable)
	logger.Debug("Executing Run()")
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// block here while the sub-runnable is running
	err := subRunnable.Run(ctx)
	if err == nil {
		// The child Run method returned without returning context cancel error.
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Filter out expected context cancellation errors on shutdown
		logger.Debug("Stopped gracefully", "reason", err)
		return
	}

	logger.Error("Returned unexpected error", "error", err)
	select {
	case r.serverErrors <- fmt.Errorf("child runnable failed %d: %w", idx, err):
	default:
		logger.Warn(
			"Failed to send error to serverErrors channel (is it full or closed?)",
			"error", err,
		)
	}
}

// stopRunnables stops all child runnables in reverse order (last to first).
func (r *Runner[T]) stopRunnables() error {
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
func (r *Runner[T]) setConfig(config *Config[T]) {
	r.currentConfig.Store(config)
	r.logger.Debug("Config updated", "config", config)
}

// getConfig returns the current configuration, loading it via the callback if necessary.
func (r *Runner[T]) getConfig() *Config[T] {
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
