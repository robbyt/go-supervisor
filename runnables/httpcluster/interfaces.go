package httpcluster

import (
	"context"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// entriesManager defines the interface for managing server entries.
// This interface allows for easy mocking in tests.
type entriesManager interface {
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
	commit() entriesManager

	// setRuntime creates a new entries collection with updated runtime state for a server.
	// This is used during the commit phase to record that a server has been started.
	// Returns nil if the server doesn't exist.
	setRuntime(
		id string,
		runner *httpserver.Runner,
		ctx context.Context,
		cancel context.CancelFunc,
		stateSub <-chan string,
	) entriesManager

	// clearRuntime creates a new entries collection with cleared runtime state for a server.
	// This is used during the commit phase to record that a server has been stopped.
	// Returns nil if the server doesn't exist.
	clearRuntime(id string) entriesManager
}
