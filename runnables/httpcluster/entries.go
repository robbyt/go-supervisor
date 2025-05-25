package httpcluster

import (
	"context"
	"log/slog"

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
	runner   *httpserver.Runner
	ctx      context.Context
	cancel   context.CancelFunc
	stateSub <-chan string

	// Pending action for commit phase
	action action
}

// entries is an immutable collection of server entries with pending actions.
// Once created, it cannot be modified - only replaced with a new instance.
type entries struct {
	servers map[string]*serverEntry
}

// newEntries creates a new entries collection from the previous state and desired configuration.
// Each entry is marked with the action needed during the commit phase.
func newEntries(
	previous *entries,
	desiredConfigs map[string]*httpserver.Config,
	logger *slog.Logger,
) *entries {
	logger = logger.WithGroup("newEntries")

	servers := make(map[string]*serverEntry)

	// Process all servers from previous state first
	if previous != nil {
		for id, oldEntry := range previous.servers {
			desiredConfig, shouldExist := desiredConfigs[id]

			if !shouldExist || desiredConfig == nil {
				// Server should be removed - copy it over marked for stopping
				if oldEntry.runner != nil {
					servers[id] = &serverEntry{
						id:       id,
						config:   oldEntry.config,
						runner:   oldEntry.runner,
						ctx:      oldEntry.ctx,
						cancel:   oldEntry.cancel,
						stateSub: oldEntry.stateSub,
						action:   actionStop,
					}
					logger.Debug("Server marked for stop", "id", id)
				}
				// If runner is nil, server was never started, so we can just skip it
			} else if oldEntry.config.Equal(desiredConfig) {
				// Config unchanged - copy as-is with no action
				servers[id] = &serverEntry{
					id:       id,
					config:   oldEntry.config,
					runner:   oldEntry.runner,
					ctx:      oldEntry.ctx,
					cancel:   oldEntry.cancel,
					stateSub: oldEntry.stateSub,
					action:   actionNone,
				}
				logger.Debug("Server unchanged", "id", id)
			} else {
				// Config changed - stop and start for simplicity
				if oldEntry.runner != nil {
					// Mark old for stop
					servers[id] = &serverEntry{
						id:       id,
						config:   oldEntry.config, // Keep old config for stop
						runner:   oldEntry.runner,
						ctx:      oldEntry.ctx,
						cancel:   oldEntry.cancel,
						stateSub: oldEntry.stateSub,
						action:   actionStop,
					}
					// Then create new entry for start
					servers[id+":new"] = &serverEntry{
						id:     id,
						config: desiredConfig,
						action: actionStart,
					}
					logger.Debug("Server marked for restart", "id", id)
				} else {
					// Server not running, just update config and mark for start
					servers[id] = &serverEntry{
						id:     id,
						config: desiredConfig,
						action: actionStart,
					}
					logger.Debug("Server marked for start", "id", id)
				}
			}
		}
	}

	// Process any new servers not in previous state
	for id, config := range desiredConfigs {
		if config == nil {
			continue
		}
		if previous == nil || previous.servers[id] == nil {
			// New server
			servers[id] = &serverEntry{
				id:     id,
				config: config,
				action: actionStart,
			}
			logger.Debug("New server marked for start", "id", id)
		}
	}

	return &entries{servers: servers}
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
// This should be called after all pending actions have been executed.
// It removes entries marked for stop and clears all action flags.
func (e *entries) commit() EntriesManager {
	servers := make(map[string]*serverEntry)

	for id, entry := range e.servers {
		switch entry.action {
		case actionStop:
			// Don't copy stopped servers
			continue
		case actionStart:
			// For servers marked with ":new" suffix, restore original ID
			actualID := id
			if len(id) > 4 && id[len(id)-4:] == ":new" {
				actualID = id[:len(id)-4]
			}
			servers[actualID] = &serverEntry{
				id:       actualID,
				config:   entry.config,
				runner:   entry.runner,
				ctx:      entry.ctx,
				cancel:   entry.cancel,
				stateSub: entry.stateSub,
				action:   actionNone,
			}
		default:
			// Copy entry with action cleared
			servers[id] = &serverEntry{
				id:       entry.id,
				config:   entry.config,
				runner:   entry.runner,
				ctx:      entry.ctx,
				cancel:   entry.cancel,
				stateSub: entry.stateSub,
				action:   actionNone,
			}
		}
	}

	return &entries{servers: servers}
}

// setRuntime creates a new entries collection with updated runtime state for a server.
// This is used during the commit phase to record that a server has been started.
// Returns nil if the server doesn't exist.
func (e *entries) setRuntime(
	id string,
	runner *httpserver.Runner,
	ctx context.Context,
	cancel context.CancelFunc,
	stateSub <-chan string,
) EntriesManager {
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
				id:       v.id,
				config:   v.config,
				runner:   runner,
				ctx:      ctx,
				cancel:   cancel,
				stateSub: stateSub,
				action:   v.action, // Preserve action
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
func (e *entries) clearRuntime(id string) EntriesManager {
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

// Ensure entries implements EntriesManager
var _ EntriesManager = (*entries)(nil)
