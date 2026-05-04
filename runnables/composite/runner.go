package composite

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor/lifecycle"
)

// ConfigCallback is the function type signature for the callback used to load initial config, and new config during Reload()
type ConfigCallback[T runnable] func() (*Config[T], error)

type fsm interface {
	GetState() string
	GetStateChan(ctx context.Context) <-chan string
	Transition(state string) error
	TransitionIfCurrentState(state string, targetState string) error
	SetState(state string) error
}

// Runner implements a component that manages multiple runnables of the same type
// as a single unit. It satisfies the Runnable, Reloadable, and Stateable interfaces.
type Runner[T runnable] struct {
	fsm            fsm
	lc             *lifecycle.StartStop
	configMu       sync.Mutex // Only used for getConfig()
	currentConfig  atomic.Pointer[Config[T]]
	configCallback ConfigCallback[T]

	runnablesMu sync.Mutex

	reloadCh     chan *reloadReq[T]
	serverErrors chan error
	logger       *slog.Logger
}

// reloadReq carries an accepted reload from Reload(ctx) into Run's event
// loop so all FSM transitions (Reloading→Running, Reloading→Error) and any
// child work happen single-threaded alongside Run's lifecycle transitions —
// eliminating the Run-vs-Reload race where Run's shutdown sequence would
// otherwise observe FSM=Reloading and trip on Reloading→Stopped (an invalid
// transition per transitions.Typical).
//
// result is the result channel: Run sends the reload outcome on it (nil on
// success, non-nil on failure) — including from drainReloadCh on Run exit,
// which sends the abandonment sentinel — so a caller blocked on the receive
// always unblocks with a meaningful value. The channel doubles as both the
// completion signal and the error carrier; no shared mutable field needed.
type reloadReq[T runnable] struct {
	cfg    *Config[T]
	result chan error
}

// NewRunner creates a new CompositeRunner instance with the provided configuration callback and options.
// Parameters:
//   - configCallback: Required. A function that returns the initial configuration and is called during any reload operations.
//   - opts: Optional. A variadic list of Option functions to customize the Runner behavior.
//
// The configCallback cannot be nil and will be invoked by Run() to load the initial configuration.
func NewRunner[T runnable](
	configCallback ConfigCallback[T],
	opts ...Option[T],
) (*Runner[T], error) {
	logger := slog.Default().WithGroup("composite.Runner")
	r := &Runner[T]{
		lc:             lifecycle.New(),
		currentConfig:  atomic.Pointer[Config[T]]{},
		configCallback: configCallback,
		reloadCh:       make(chan *reloadReq[T], 1),
		serverErrors:   make(chan error, 1),
		logger:         logger,
	}

	// Apply options, to override defaults if provided
	for _, opt := range opts {
		opt(r)
	}

	// Validate configuration
	if r.configCallback == nil {
		return nil, fmt.Errorf(
			"%w: config callback is required",
			ErrCompositeRunnable,
		)
	}

	// Create FSM after the optional logger has been configured
	fsm, err := finitestate.NewTypicalFSM(r.logger.WithGroup("fsm").Handler())
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
	// Defer order matters: drainReloadCh runs LAST so it catches any reload
	// request that arrived after lc.done() closed DoneCh but before this
	// function returned. lc.done() runs before drainReloadCh so DoneCh-based
	// callers see the runner as stopped while we drain stragglers.
	defer r.drainReloadCh()
	done := r.lc.Started()
	defer done()

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Transition from New to Booting
	if err := r.fsm.Transition(finitestate.StatusBooting); err != nil {
		return fmt.Errorf("failed to transition to Booting state: %w", err)
	}

	// Start all child runnables
	if err := r.boot(runCtx); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to start child runnables: %w", err)
	}

	// Transition from Booting to Running
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to Running state: %w", err)
	}

	if err := r.waitForEvent(runCtx); err != nil {
		return err
	}
	runCancel()

	r.drainReloadCh()

	if err := r.fsm.TransitionIfCurrentState(finitestate.StatusRunning, finitestate.StatusStopping); err != nil {
		// This error is expected if we're already stopping, so only log at debug level
		r.logger.Debug("Not transitioning to Stopping state", "error", err)
	}

	// Stop all child runnables
	if err := r.stopAllRunnables(); err != nil {
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

// Stop signals the runner to shut down and blocks until Run() completes.
func (r *Runner[T]) Stop() {
	r.lc.Stop()
}

// waitForEvent blocks until context cancellation, stop signal, or a child error.
// Reload requests are processed inline via reloadCh so the FSM transition
// Reloading→Running and any child work happen on Run's goroutine, single-
// threaded with Run's lifecycle transitions.
func (r *Runner[T]) waitForEvent(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("Local context canceled")
			return nil
		case <-r.lc.StopCh():
			r.logger.Debug("Stop() called")
			return nil
		case err := <-r.serverErrors:
			r.setStateError()
			r.drainReloadCh()
			stopErr := r.stopAllRunnables()
			return fmt.Errorf("%w: %w", ErrRunnableFailed, errors.Join(err, stopErr))
		case req := <-r.reloadCh:
			req.result <- r.handleReload(ctx, req.cfg)
		}
	}
}

// handleReload runs an accepted reload inside Run's goroutine. Reload has
// already moved the FSM Running→Reloading; this completes the work and
// transitions the FSM back to Running (or sets Error on failure). The
// membership-change vs in-place decision is made here, against the runner's
// current config — keeping that decision on Run's side guarantees it sees a
// consistent old/new pair. Returns the reload outcome so waitForEvent owns
// the channel-protocol step (req.err = err; close(req.done)).
func (r *Runner[T]) handleReload(ctx context.Context, cfg *Config[T]) error {
	oldConfig := r.getConfig()
	if oldConfig == nil {
		r.logger.Warn("No current config during reload, treating as empty")
		oldConfig = &Config[T]{}
	}

	var err error
	if hasMembershipChanged(oldConfig, cfg) {
		err = r.reloadWithRestart(ctx, cfg)
	} else {
		err = r.reloadSkipRestart(ctx, cfg)
	}

	if err != nil {
		r.logger.Error("Reload failed", "error", err)
		r.setStateError()
		return err
	}

	if transErr := r.fsm.Transition(finitestate.StatusRunning); transErr != nil {
		r.logger.Error("Failed to transition Reloading→Running", "error", transErr)
		r.setStateError()
		return transErr
	}
	return nil
}

// drainReloadCh sends an abandonment error on req.result for any reload
// request still buffered in reloadCh after Run's select loop exits. Without
// this, a Reload caller blocked on the receive would only unblock via
// lc.DoneCh() in its outer select — sending here makes the protocol explicit
// and unblocks the caller's result branch deterministically.
//
// Also transitions the FSM Reloading→Running before sending. Run is the
// single FSM mutator during reload, so this best-effort transition (a no-op
// if FSM isn't Reloading) keeps the runner's subsequent Stopping/Stopped
// transitions valid: Reloading→Stopped is not a legal transition per the FSM
// table, but Running→Stopping→Stopped is.
func (r *Runner[T]) drainReloadCh() {
	for {
		select {
		case req := <-r.reloadCh:
			if err := r.fsm.TransitionIfCurrentState(
				finitestate.StatusReloading, finitestate.StatusRunning,
			); err != nil {
				r.logger.Debug("drainReloadCh: FSM not in Reloading", "error", err)
			}
			// Surface the abandonment via req.result so Reload's
			// <-req.result branch returns a non-nil error per T3.1.
			// Without this, an accepted-then-drained reload would
			// silently look like success.
			req.result <- errors.New("runner stopped before reload was handled")
		default:
			return
		}
	}
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

	// If there are no entries, log and return without error
	if len(cfg.Entries) == 0 {
		logger.Debug("No runnables found in configuration")
		return nil
	}

	// Grow the error channel so every child can report without dropping
	if len(cfg.Entries) > cap(r.serverErrors) {
		r.serverErrors = make(chan error, len(cfg.Entries))
	}

	logger.Debug("Starting child runnables...", "count", len(cfg.Entries))

	// Use a temporary WaitGroup to track that all goroutines have started.
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

// stopAllRunnables stops all child runnables in reverse order (last to first).
func (r *Runner[T]) stopAllRunnables() error {
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
