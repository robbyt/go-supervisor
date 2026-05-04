package composite

import (
	"context"
	"fmt"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// ReloadableWithConfig is an interface for sub-runnables that can reload with
// a specific configuration. Implementations should honor ctx — at minimum to
// abort the reload if the caller cancels — but may also use it for deadline
// propagation if the reload itself is bounded. ctx must be non-nil; callers
// without a meaningful ctx should pass context.Background() or context.TODO().
// Like Reloadable.Reload, this returns no error: failures should surface via
// the runnable's own logging or via Stateable.GetStateChan (e.g. transitioning
// to an Error state).
type ReloadableWithConfig interface {
	ReloadWithConfig(ctx context.Context, config any)
}

// Reload updates the configuration and handles runnables appropriately. If
// membership changes (different set of runnables), all existing runnables are
// stopped and the new set is started.
//
// Concurrent callers are admitted via the FSM: the atomic Running→Reloading
// transition is the single-flight gate. A second caller arriving while a
// reload is already in flight observes Reloading, fails the transition, and
// returns without queueing.
//
// Once admitted, the work is dispatched to Run's event loop, which owns the
// FSM transition back to Running (or to Error on failure). This keeps Run as
// the single FSM mutator during reload, eliminating the Run-vs-Reload race
// where Run's shutdown sequence could otherwise observe FSM=Reloading. The
// caller's ctx is admission-only: it cancels the request before it has been
// accepted by Run's loop. Once accepted, Reload waits for the work to finish
// — caller's ctx is intentionally ignored at that point so the caller never
// observes FSM=Reloading after Reload returns.
//
// Cleanup follows httpserver's per-branch pattern (no catch-all defer): each
// abort path that leaves FSM=Reloading transitions it back explicitly. A
// catch-all defer would race the next admission gate — after handleReload
// completed Reloading→Running and a new caller won the gate, the previous
// caller's defer could fire `TIC(Reloading→Running)` and erroneously release
// the new caller's gate, derailing the new request's Run loop transition.
func (r *Runner[T]) Reload(ctx context.Context) {
	logger := r.logger.WithGroup("Reload")

	// Fast-path: if the caller's ctx is already cancelled, bail before
	// touching the FSM or invoking configCallback. Without this, the select
	// in dispatchReload can race ctx.Done() against the reloadCh send and
	// dispatch a request the caller has already abandoned.
	select {
	case <-ctx.Done():
		logger.Debug("Reload caller ctx done before dispatch", "error", ctx.Err())
		return
	default:
	}

	if err := r.fsm.TransitionIfCurrentState(
		finitestate.StatusRunning,
		finitestate.StatusReloading,
	); err != nil {
		// Either another reload is already in flight (FSM in Reloading) or the
		// runner is in Stopping/Stopped/Booting/Error. Either way, drop this
		// request — there is no failure of *this* reload to surface, so don't
		// move the FSM to Error.
		logger.Debug("Cannot reload — runner not in Running state",
			"current", r.fsm.GetState(), "error", err)
		return
	}

	newConfig, err := r.configCallback()
	if err != nil {
		logger.Error("config callback failed", "error", err)
		r.setStateError()
		return
	}
	if newConfig == nil {
		logger.Error("config callback returned nil")
		r.setStateError()
		return
	}

	r.dispatchReload(ctx, newConfig)
}

// dispatchReload sends an accepted reload to Run's event loop and waits for
// completion. On the happy path, handleReload owns the FSM transition
// Reloading→Running; on the shutdown path, drainReloadCh owns it. On either
// abort path before send (caller's ctx fires, runner stops), this function
// transitions the FSM back to Running explicitly. Mirrors httpserver's
// per-branch cleanup so we never leave a catch-all defer racing the next
// caller's admission gate.
func (r *Runner[T]) dispatchReload(ctx context.Context, newConfig *Config[T]) {
	req := &reloadReq[T]{cfg: newConfig, done: make(chan struct{})}
	select {
	case r.reloadCh <- req:
		// Accepted. Wait for Run to finish (or for drainReloadCh to close
		// done if Run exits before processing).
		<-req.done
	case <-ctx.Done():
		r.abortDispatch("Reload caller ctx done before dispatch", "error", ctx.Err())
	case <-r.lc.DoneCh():
		r.abortDispatch("Runner stopped before reload dispatch")
	}
}

// abortDispatch is the per-branch cleanup for dispatchReload's abort paths.
// Reload moved FSM Running→Reloading at admission; we must put it back to
// Running before returning so the next admission gate sees a clean state. If
// the transition fails (e.g., FSM was already moved by a concurrent shutdown
// or setStateError), escalate to Error: by this point the FSM is in some
// terminal-leaning state and a stuck Reloading is the worst outcome.
func (r *Runner[T]) abortDispatch(reason string, attrs ...any) {
	logger := r.logger.WithGroup("dispatchReload")
	logger.Debug(reason, attrs...)
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		logger.Error("Failed to transition Reloading→Running", "error", err)
		r.setStateError()
	}
}

// reloadWithRestart handles the case where the membership of runnables has changed.
func (r *Runner[T]) reloadWithRestart(ctx context.Context, newConfig *Config[T]) error {
	logger := r.logger.WithGroup("reloadWithRestart")
	logger.Debug("Reloading runnables due to membership change")
	defer logger.Debug("Completed.")

	// Stop all existing runnables while we still have the old config
	// This acquires the runnables mutex
	if err := r.stopAllRunnables(); err != nil {
		return fmt.Errorf("%w: failed to stop existing runnables during membership change", err)
	}
	// Now update the stored config after stopping old runnables
	// Lock the config mutex for writing
	logger.Debug("Updating config after stopping existing runnables")
	r.configMu.Lock()
	r.setConfig(newConfig)
	r.configMu.Unlock()

	// Start all runnables from the new config
	// This acquires the runnables mutex
	if err := r.boot(ctx); err != nil {
		return fmt.Errorf("%w: failed to start new runnables during membership change", err)
	}
	return nil
}

// reloadSkipRestart handles the case where the membership of runnables has not
// changed. Called from Run's event loop (handleReload) so the inline child
// reloads execute on Run's goroutine, single-threaded with Run's lifecycle
// transitions. Returns nil today — the signature returns error so it can be
// dispatched alongside reloadWithRestart through the same handleReload path.
func (r *Runner[T]) reloadSkipRestart(ctx context.Context, newConfig *Config[T]) error {
	logger := r.logger.WithGroup("reloadSkipRestart")
	logger.Debug("Reloading runnables without membership change")
	defer logger.Debug("Completed.")

	logger.Debug("Updating config")
	r.configMu.Lock()
	r.setConfig(newConfig)
	r.configMu.Unlock()

	logger.Debug("Reloading configs of existing runnables")
	// Reload configs of existing runnables
	// Runnables mutex not locked as membership is not changing
	for _, entry := range newConfig.Entries {
		logger := logger.With("runnable", entry.Runnable.String())

		if reloadableWithConfig, ok := any(entry.Runnable).(ReloadableWithConfig); ok {
			// If the runnable implements our ReloadableWithConfig interface, use that to pass the new config
			logger.Debug("Reloading child runnable with config")
			reloadableWithConfig.ReloadWithConfig(ctx, entry.Config)
		} else if reloadable, ok := any(entry.Runnable).(supervisor.Reloadable); ok {
			// Fall back to standard Reloadable interface, assume the configCallback
			// has somehow updated the runnable's internal state
			logger.Debug("Reloading child runnable")
			reloadable.Reload(ctx)
		} else {
			logger.Warn("Child runnable does not implement Reloadable or ReloadableWithConfig")
		}
	}
	return nil
}

// hasMembershipChanged checks if the set of runnables has changed between configurations
func hasMembershipChanged[T runnable](oldConfig, newConfig *Config[T]) bool {
	if len(oldConfig.Entries) != len(newConfig.Entries) {
		// Different number of entries means membership changed
		return true
	}

	// Create a map of old runnables by their string representation
	oldMap := make(map[string]bool)
	for _, entry := range oldConfig.Entries {
		oldMap[entry.Runnable.String()] = true
	}

	// Check if any new runnable is not in the old set
	for _, entry := range newConfig.Entries {
		if !oldMap[entry.Runnable.String()] {
			return true
		}
	}

	return false
}
