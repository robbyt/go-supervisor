package httpcluster

import (
	"context"
	"log/slog"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// EntriesManager defines the interface for managing server entries.
// This interface allows for easy mocking in tests.
type EntriesManager interface {
	// get returns a server entry by ID, or nil if not found.
	get(id string) *serverEntry

	// count returns the total number of server entries.
	count() int

	// countByAction returns the number of entries with the specified action.
	countByAction(a action) int

	// getPendingActions returns lists of server IDs grouped by their pending action.
	getPendingActions() (toStart, toStop []string)

	// commit creates a new entries collection with all actions marked as complete.
	// This should be called after all pending actions have been executed.
	// It removes entries marked for stop and clears all action flags.
	commit() EntriesManager

	// setRuntime creates a new entries collection with updated runtime state for a server.
	// This is used during the commit phase to record that a server has been started.
	// Returns nil if the server doesn't exist.
	setRuntime(
		id string,
		runner *httpserver.Runner,
		ctx context.Context,
		cancel context.CancelFunc,
		stateSub <-chan string,
	) EntriesManager

	// clearRuntime creates a new entries collection with cleared runtime state for a server.
	// This is used during the commit phase to record that a server has been stopped.
	// Returns nil if the server doesn't exist.
	clearRuntime(id string) EntriesManager
}

// NewEntriesManager creates a new entries manager from the previous state and desired configuration.
// Each entry is marked with the action needed during the commit phase.
func NewEntriesManager(
	previous EntriesManager,
	desiredConfigs map[string]*httpserver.Config,
	logger *slog.Logger,
) EntriesManager {
	var prev *entries
	if previous != nil {
		// Convert interface back to concrete type
		if concrete, ok := previous.(*entries); ok {
			prev = concrete
		}
	}

	return newEntries(prev, desiredConfigs, logger)
}
