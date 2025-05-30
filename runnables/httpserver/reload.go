package httpserver

import (
	"errors"
	"fmt"

	"github.com/robbyt/go-supervisor/internal/finitestate"
)

// reloadConfig reloads the configuration using the config callback
func (r *Runner) reloadConfig() error {
	newConfig, err := r.configCallback()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrConfigCallback, err)
	}

	if newConfig == nil {
		return ErrConfigCallbackNil
	}

	oldConfig := r.getConfig()
	if oldConfig == nil {
		r.setConfig(newConfig)
		r.logger.Debug("Config loaded", "newConfig", newConfig)
		return nil
	}

	if newConfig.Equal(oldConfig) {
		// Config unchanged, skip reload and return early
		return ErrOldConfig
	}

	r.setConfig(newConfig)
	r.logger.Debug("Config reloaded", "newConfig", newConfig)
	return nil
}

// Reload refreshes the server configuration and restarts the HTTP server if necessary.
// This method is safe to call while the server is running and will handle graceful shutdown and restart.
func (r *Runner) Reload() {
	r.logger.Debug("Reloading...")
	r.mutex.Lock()
	defer r.mutex.Unlock()

	if err := r.fsm.Transition(finitestate.StatusReloading); err != nil {
		r.logger.Error("Failed to transition to Reloading", "error", err)
		return
	}

	err := r.reloadConfig()
	switch {
	case err == nil:
		r.logger.Debug("Config reloaded")
	case errors.Is(err, ErrOldConfig):
		r.logger.Debug("Config unchanged, skipping reload")
		if stateErr := r.fsm.Transition(finitestate.StatusRunning); stateErr != nil {
			r.logger.Error("Failed to transition to Running", "error", stateErr)
			r.setStateError()
		}
		return
	default:
		r.logger.Error("Failed to reload configuration", "error", err)
		r.setStateError()
		return
	}

	if err := r.stopServer(r.ctx); err != nil {
		r.logger.Error("Failed to stop server during reload", "error", err)
		r.setStateError()
		return
	}

	if err := r.boot(); err != nil {
		r.logger.Error("Failed to boot server during reload", "error", err)
		r.setStateError()
		return
	}

	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.logger.Error("Failed to transition to Running", "error", err)
		r.setStateError()
		return
	}

	r.logger.Debug("Completed.")
}
