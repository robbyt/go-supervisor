package composite

import (
	"context"
	"errors"
	"fmt"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// ReloadableWithConfig is an interface for sub-runnables that can reload with specific config
type ReloadableWithConfig interface {
	ReloadWithConfig(config any)
}

// Reload updates the configuration and handles runnables appropriately.
// If membership changes (different set of runnables), all existing runnables are stopped
// and the new set of runnables is started.
func (r *Runner[T]) Reload(ctx context.Context) {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	logger := r.logger.WithGroup("Reload")

	if err := r.fsm.Transition(finitestate.StatusReloading); err != nil {
		logger.Error("Failed to transition to Reloading", "error", err)
		r.setStateError()
		return
	}

	if err := r.doReload(ctx); err != nil {
		logger.Error("Reload failed", "error", err)
		r.setStateError()
		return
	}

	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		logger.Error("Failed to transition to Running", "error", err)
		r.setStateError()
		return
	}
}

// doReload loads the new configuration and routes to the appropriate reload strategy.
func (r *Runner[T]) doReload(ctx context.Context) error {
	newConfig, err := r.configCallback()
	if err != nil {
		return fmt.Errorf("config callback: %w", err)
	}
	if newConfig == nil {
		return errors.New("config callback returned nil")
	}

	oldConfig := r.getConfig()
	if oldConfig == nil {
		r.logger.Warn("No current config during reload, treating as empty")
		oldConfig = &Config[T]{}
	}

	if hasMembershipChanged(oldConfig, newConfig) {
		return r.dispatchMembershipReload(ctx, newConfig)
	}

	r.reloadSkipRestart(ctx, newConfig)
	return nil
}

// dispatchMembershipReload sends a membership-change reload to Run's event loop
// so new children boot into runCtx's cancellation tree. Aborts cleanly if the
// runner has already exited or the caller's context is cancelled, so callers
// never hang on a request that nobody will receive.
func (r *Runner[T]) dispatchMembershipReload(ctx context.Context, newConfig *Config[T]) error {
	req := reloadRequest[T]{newConfig: newConfig, done: make(chan error, 1)}
	select {
	case r.reloadCh <- req:
		select {
		case err := <-req.done:
			return err
		case <-ctx.Done():
			return fmt.Errorf("reload cancelled: %w", ctx.Err())
		case <-r.lc.DoneCh():
			return errors.New("runner stopped while reload pending")
		}
	case <-ctx.Done():
		return fmt.Errorf("reload cancelled: %w", ctx.Err())
	case <-r.lc.DoneCh():
		return errors.New("runner stopped before reload")
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

// reloadSkipRestart handles the case where the membership of runnables has not changed.
func (r *Runner[T]) reloadSkipRestart(ctx context.Context, newConfig *Config[T]) {
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
			reloadableWithConfig.ReloadWithConfig(entry.Config)
		} else if reloadable, ok := any(entry.Runnable).(supervisor.Reloadable); ok {
			// Fall back to standard Reloadable interface, assume the configCallback
			// has somehow updated the runnable's internal state
			logger.Debug("Reloading child runnable")
			reloadable.Reload(ctx)
		} else {
			logger.Warn("Child runnable does not implement Reloadable or ReloadableWithConfig")
		}
	}
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
