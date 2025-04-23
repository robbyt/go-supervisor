package composite

import (
	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// ReloadableWithConfig is an interface for sub-runnables that can reload with specific config
type ReloadableWithConfig interface {
	ReloadWithConfig(config any)
}

// Reload updates the configuration and handles runnables appropriately.
// If membership changes (different set of runnables), all existing runnables are stopped
// and the new set is started to ensure proper lifecycle management.
func (r *Runner[T]) Reload() {
	logger := r.logger.WithGroup("Reload")
	logger.Debug("Reloading...")
	defer func() {
		logger.Debug("Completed.")
	}()

	// Transition to Reloading state
	if err := r.fsm.Transition(finitestate.StatusReloading); err != nil {
		logger.Error("Failed to transition to Reloading", "error", err)
		r.setStateError()
		return
	}

	// Get updated config from the callback function
	newConfig, err := r.configCallback()
	if err != nil {
		logger.Error("Failed to get updated config", "error", err)
		r.setStateError()
		return
	}
	if newConfig == nil {
		logger.Error("Config callback returned nil during reload")
		r.setStateError()
		return
	}

	// Get the old config to compare
	oldConfig := r.getConfig()
	if oldConfig == nil {
		logger.Error("Failed to get current config during reload")
		r.setStateError()
		return
	}

	// Check if membership has changed by comparing runnable identities
	if hasMembershipChanged(oldConfig, newConfig) {
		logger.Debug(
			"Membership change detected, stopping all existing runnables before updating config",
		)

		// Stop all existing runnables while we still have the old config
		// This acquires the runnables mutex
		if err := r.stopRunnables(); err != nil {
			logger.Error(
				"Failed to stop existing runnables during membership change",
				"error", err,
			)
			r.setStateError()
			return
		}

		// Now update the stored config after stopping old runnables
		// Lock the config mutex for writing
		r.configMu.Lock()
		r.setConfig(newConfig)
		r.configMu.Unlock()

		// Start all runnables from the new config
		// This acquires the runnables mutex
		// Note: this uses the parent context of the runner instead of the context sent to `Run()`
		if err := r.boot(r.ctx); err != nil {
			logger.Error("Failed to start new runnables during membership change", "error", err)
			r.setStateError()
			return
		}

		logger.Debug("Successfully restarted all runnables after membership change")
	} else {
		// No membership change, just update config and reload existing runnables
		r.configMu.Lock()
		r.setConfig(newConfig)
		r.configMu.Unlock()

		// Reload configs of existing runnables
		// No need to lock the runnables mutex as we're not changing membership
		reloaded := 0
		for _, entry := range newConfig.Entries {
			if reloadableWithConfig, ok := any(entry.Runnable).(ReloadableWithConfig); ok {
				// If the runnable implements our ReloadableWithConfig interface, use that to pass the new config
				logger.Debug("Reloading child runnable with config", "runnable", entry.Runnable)
				reloadableWithConfig.ReloadWithConfig(entry.Config)
				reloaded++
			} else if reloadable, ok := any(entry.Runnable).(supervisor.Reloadable); ok {
				// Fall back to standard Reloadable interface, assume the configCallback
				// has somehow updated the runnable's internal state
				logger.Debug("Reloading child runnable", "runnable", entry.Runnable)
				reloadable.Reload()
				reloaded++
			}
		}
		logger.Debug("Reloaded runnables without membership change", "runnablesReloaded", reloaded)
	}

	// Transition back to Running
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		logger.Error("Failed to transition to Running", "error", err)
		r.setStateError()
		return
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
