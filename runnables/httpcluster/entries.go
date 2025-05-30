package httpcluster

import (
	"context"
	"iter"
	"maps"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// action represents the pending action for a server entry.
type action string

const (
	actionNone  action = "none"  // No change needed
	actionStart action = "start" // Server needs to be started
	actionStop  action = "stop"  // Server needs to be stopped
)

// serverEntry represents a server entry with configuration, runtime state, and pending action.
type serverEntry struct {
	id     string
	config *httpserver.Config

	// Runtime state - nil if server is not running
	runner httpServerRunner
	ctx    context.Context
	cancel context.CancelFunc

	// Pending action for the commit phase
	action action
}

// Ensure the entries collection object implements entriesManager, used by the Runnable
var _ entriesManager = (*entries)(nil)

// entries is an immutable collection of server entries with pending actions.
// Once created, it is not modified - only replaced with a new instance.
type entries struct {
	servers map[string]*serverEntry
}

// newEntries creates a new entries collection from desired configuration.
// This is used for initial creation only.
func newEntries(desiredConfigs map[string]*httpserver.Config) *entries {
	servers := make(map[string]*serverEntry)

	for id, config := range desiredConfigs {
		if config == nil {
			continue
		}
		servers[id] = &serverEntry{
			id:     id,
			config: config,
			action: actionStart,
		}
	}

	return &entries{servers: servers}
}

// removeEntry creates a new entries collection with the specified entry removed.
func (e *entries) removeEntry(id string) entriesManager {
	_, exists := e.servers[id]
	if !exists {
		return e
	}

	newServers := make(map[string]*serverEntry, len(e.servers)-1)
	for k, v := range e.servers {
		if k != id {
			newServers[k] = v
		}
	}

	return &entries{servers: newServers}
}

// getPendingActions returns lists of server IDs grouped by their pending action.
func (e *entries) getPendingActions() (toStart, toStop []string) {
	for id, entry := range e.servers {
		switch entry.action {
		case actionStart:
			toStart = append(toStart, id)
		case actionStop:
			toStop = append(toStop, id)
		}
	}
	return
}

// get returns a server entry by ID, or nil if not found.
func (e *entries) get(id string) *serverEntry {
	return e.servers[id]
}

// count returns the total number of server entries.
func (e *entries) count() int {
	return len(e.servers)
}

// countByAction returns the number of entries with the specified action.
func (e *entries) countByAction(a action) int {
	count := 0
	for _, entry := range e.servers {
		if entry.action == a {
			count++
		}
	}
	return count
}

// commit creates a new entries collection with all actions marked as complete.
// Called after all pending actions have been executed.
// It removes entries marked for stop and clears all action flags.
func (e *entries) commit() entriesManager {
	servers := make(map[string]*serverEntry)

	for id, entry := range e.servers {
		if entry.action == actionStop {
			// Don't copy stopped servers
			continue
		}
		// Copy entry with action cleared
		servers[id] = &serverEntry{
			id:     entry.id,
			config: entry.config,
			runner: entry.runner,
			ctx:    entry.ctx,
			cancel: entry.cancel,
			action: actionNone,
		}
	}

	return &entries{servers: servers}
}

// setRuntime creates a new entries collection with updated runtime state for a server.
// This is used during the commit phase to record that a server has been started.
// Returns nil if the server doesn't exist.
func (e *entries) setRuntime(
	id string,
	runner httpServerRunner,
	ctx context.Context,
	cancel context.CancelFunc,
) entriesManager {
	_, exists := e.servers[id]
	if !exists {
		return nil
	}

	// Create new entries with all the same servers
	newServers := make(map[string]*serverEntry, len(e.servers))
	for k, v := range e.servers {
		if k == id {
			// Create new entry with updated runtime
			newServers[k] = &serverEntry{
				id:     v.id,
				config: v.config,
				runner: runner,
				ctx:    ctx,
				cancel: cancel,
				action: v.action, // Preserve action
			}
		} else {
			// Copy existing entry
			newServers[k] = v
		}
	}

	return &entries{servers: newServers}
}

// clearRuntime creates a new entries collection with cleared runtime state for a server.
// This is used during the commit phase to record that a server has been stopped.
// Returns nil if the server doesn't exist.
func (e *entries) clearRuntime(id string) entriesManager {
	_, exists := e.servers[id]
	if !exists {
		return nil
	}

	// Create new entries with all the same servers
	newServers := make(map[string]*serverEntry, len(e.servers))
	for k, v := range e.servers {
		if k == id {
			// Create new entry with cleared runtime
			newServers[k] = &serverEntry{
				id:     v.id,
				config: v.config,
				action: v.action, // Preserve action
				// Runtime fields set to nil
			}
		} else {
			// Copy existing entry
			newServers[k] = v
		}
	}

	return &entries{servers: newServers}
}

// processExistingServer handles the logic for processing a server from the previous state.
// Returns an iterator that yields (key, *serverEntry) pairs to add to the servers map.
func processExistingServer(
	id string,
	oldEntry *serverEntry,
	desiredConfig *httpserver.Config,
) iter.Seq2[string, *serverEntry] {
	return func(yield func(string, *serverEntry) bool) {
		if oldEntry == nil {
			return
		}

		// Case 1: Server is removed
		if desiredConfig == nil {
			if oldEntry.runner != nil {
				newEntry := *oldEntry
				newEntry.action = actionStop
				yield(id, &newEntry)
			}
			// If runner is nil, server was never started, so skip it
			return
		}

		// Case 2: Config unchanged
		if oldEntry.config.Equal(desiredConfig) {
			newEntry := *oldEntry
			newEntry.action = actionNone
			yield(id, &newEntry)
			return
		}

		// Case 3: Config changed - need to restart
		if oldEntry.runner != nil {
			// Running server marked for restart (stop then start)
			stopEntry := *oldEntry
			stopEntry.action = actionStop

			if !yield(id+":stop", &stopEntry) {
				return
			}

			startEntry := &serverEntry{
				id:     id,
				config: desiredConfig,
				action: actionStart,
			}

			yield(id, startEntry)
			return
		}

		// Not running, just start with new config
		yield(id, &serverEntry{
			id:     id,
			config: desiredConfig,
			action: actionStart,
		})
	}
}

// buildPendingEntries creates a new entries collection based on the desired state.
// It uses the current entries as the previous state and applies the same logic as newEntries.
func (e *entries) buildPendingEntries(desired entriesManager) entriesManager {
	// Extract configs from the desired entries
	desiredEntries, ok := desired.(*entries)
	if !ok {
		return e
	}

	desiredConfigs := make(map[string]*httpserver.Config)
	for id, entry := range desiredEntries.servers {
		desiredConfigs[id] = entry.config
	}

	servers := make(map[string]*serverEntry)

	// Process existing servers (mark for stop, or update)
	for id, oldEntry := range e.servers {
		maps.Insert(servers, processExistingServer(id, oldEntry, desiredConfigs[id]))
	}

	// Process new servers
	for id, config := range desiredConfigs {
		if config == nil {
			continue
		}
		if e.servers[id] == nil {
			// New server
			servers[id] = &serverEntry{
				id:     id,
				config: config,
				action: actionStart,
			}
		}
	}

	return &entries{servers: servers}
}
