package composite

import (
	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// Reload updates the configuration and tells any child runnables that support
// the Reloadable interface to reload themselves, passing the appropriate configuration.
func (r *CompositeRunner[T]) Reload() {
	// Acquire lock to prevent concurrent modifications
	r.runnablesMu.Lock()
	defer r.runnablesMu.Unlock()

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

	// Only update if the config has actually changed
	oldConfig := r.getConfig()
	configUpdated := oldConfig == nil || !oldConfig.Equal(newConfig)

	if configUpdated {
		r.setConfig(newConfig)
		r.logger.Debug("Updated configuration during reload")
	}

	// Reload child runnables with their specific configs
	reloaded := 0
	for _, entry := range newConfig.Entries {
		// First check if the runnable implements our enhanced ReloadableWithConfig interface
		if reloadableWithConfig, ok := any(entry.Runnable).(ReloadableWithConfig); ok {
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

	// Transition back to Running
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.logger.Error("Failed to transition to Running", "error", err)
		r.setStateError()
		return
	}

	r.logger.Debug(
		"Reload completed",
		"configUpdated",
		configUpdated,
		"runnablesReloaded",
		reloaded,
	)
}
