package composite

import (
	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// Reload updates the configuration and handles runnables appropriately.
// If membership changes (different set of runnables), all existing runnables are stopped
// and the new set is started to ensure proper lifecycle management.
func (r *CompositeRunner[T]) Reload() {
	r.logger.Debug("Reloading composite runner...")

	// Transition to Reloading state
	if err := r.fsm.Transition(finitestate.StatusReloading); err != nil {
		r.logger.Error("Failed to transition to Reloading", "error", err)
		r.setStateError()
		return
	}

	// Get updated config from callback
	newConfig, err := r.configCallback()
	if err != nil {
		r.logger.Error("Failed to get updated config", "error", err)
		r.setStateError()
		return
	}

	if newConfig == nil {
		r.logger.Error("Config callback returned nil during reload")
		r.setStateError()
		return
	}

	// Get the old config to compare
	oldConfig := r.getConfig()
	if oldConfig == nil {
		r.logger.Error("Failed to get current config during reload")
		r.setStateError()
		return
	}

	// Check if membership has changed by comparing runnable identities
	membershipChanged := hasMembershipChanged(oldConfig, newConfig)

	if membershipChanged {
		r.logger.Debug(
			"Membership change detected, stopping all existing runnables before updating config",
		)

		// Stop all existing runnables while we still have the old config
		// This acquires the runnables mutex
		if err := r.stopRunnables(); err != nil {
			r.logger.Error(
				"Failed to stop existing runnables during membership change",
				"error",
				err,
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
		if err := r.boot(); err != nil {
			r.logger.Error("Failed to start new runnables during membership change", "error", err)
			r.setStateError()
			return
		}

		r.logger.Debug("Successfully restarted all runnables after membership change")
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
				// If the runnable implements the ReloadableWithConfig interface, use that to pass the new config
				r.logger.Debug("Reloading child runnable with config", "runnable", entry.Runnable)
				reloadableWithConfig.ReloadWithConfig(entry.Config)
				reloaded++
			} else if reloadable, ok := any(entry.Runnable).(supervisor.Reloadable); ok {
				// Fall back to standard Reloadable interface
				r.logger.Debug("Reloading child runnable", "runnable", entry.Runnable)
				reloadable.Reload()
				reloaded++
			}
		}

		r.logger.Debug("Reloaded runnables without membership change", "runnablesReloaded", reloaded)
	}

	// Transition back to Running
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.logger.Error("Failed to transition to Running", "error", err)
		r.setStateError()
		return
	}

	r.logger.Debug("Reload completed successfully", "membershipChanged", membershipChanged)
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
