package httpcluster

import (
	"context"
	"net/http"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestHTTPConfig creates a test httpserver config
func createTestHTTPConfig(t *testing.T, addr string) *httpserver.Config {
	t.Helper()
	route, err := httpserver.NewRouteFromHandlerFunc(
		"test",
		"/",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	routes := httpserver.Routes{*route}
	config, err := httpserver.NewConfig(addr, routes)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

// createTestServerEntry creates a test server entry with runtime state
func createTestServerEntry(
	t *testing.T,
	id string,
	config *httpserver.Config,
	withRuntime bool,
) *serverEntry {
	t.Helper()
	entry := &serverEntry{
		id:     id,
		config: config,
		action: actionNone,
	}

	if withRuntime {
		ctx, cancel := context.WithCancel(context.Background())
		// Set minimal runtime state for testing
		entry.ctx = ctx
		entry.cancel = cancel
		// Set a non-nil runner to indicate the server is running
		entry.runner = &httpserver.Runner{}
	}

	return entry
}

func TestNewEntries_EmptyPrevious(t *testing.T) {
	t.Run("nil previous state", func(t *testing.T) {
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
			"server2": createTestHTTPConfig(t, ":8002"),
		}

		entries := newEntries(configs)

		assert.Equal(t, 2, entries.count(), "Should have 2 entries")

		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 2, "Both servers should be marked for start")
		assert.Empty(t, toStop, "No servers should be marked for stop")
		assert.Contains(t, toStart, "server1", "server1 should be marked for start")
		assert.Contains(t, toStart, "server2", "server2 should be marked for start")

		entry1 := entries.get("server1")
		require.NotNil(t, entry1, "server1 entry should exist")
		assert.Equal(t, "server1", entry1.id, "Entry should have correct ID")
		assert.Equal(t, actionStart, entry1.action, "Entry should be marked for start")
		assert.Nil(t, entry1.runner, "New entry should not have runtime state")
	})

	t.Run("empty previous state", func(t *testing.T) {
		previous := &entries{servers: make(map[string]*serverEntry)}
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}
		desiredEntries := newEntries(configs)

		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 1, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Empty(t, toStop)
		assert.Contains(t, toStart, "server1")
	})
}

func TestNewEntries_ServerRemoval(t *testing.T) {
	t.Run("remove running server", func(t *testing.T) {
		// Setup previous state with running server
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					true,
				),
			},
		}

		// New config without server1
		configs := map[string]*httpserver.Config{}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 1, entries.count()) // Server marked for stop
		toStart, toStop := entries.getPendingActions()
		assert.Empty(t, toStart)
		assert.Len(t, toStop, 1)
		assert.Contains(t, toStop, "server1")

		entry := entries.get("server1")
		require.NotNil(t, entry)
		assert.Equal(t, actionStop, entry.action)
		assert.NotNil(t, entry.runner, "Runtime state should be preserved for stopping")
	})

	t.Run("remove non-running server", func(t *testing.T) {
		// Setup previous state with non-running server
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					false,
				),
			},
		}

		// New config without server1
		configs := map[string]*httpserver.Config{}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 0, entries.count()) // Server not copied (no runtime to stop)
		toStart, toStop := entries.getPendingActions()
		assert.Empty(t, toStart)
		assert.Empty(t, toStop)
	})

	t.Run("remove with nil config", func(t *testing.T) {
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					true,
				),
			},
		}

		// Explicit nil config for server1
		configs := map[string]*httpserver.Config{
			"server1": nil,
		}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 1, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Empty(t, toStart)
		assert.Len(t, toStop, 1)
		assert.Contains(t, toStop, "server1")
	})
}

func TestNewEntries_ServerAddition(t *testing.T) {
	t.Run("add new server", func(t *testing.T) {
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					true,
				),
			},
		}

		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"), // Unchanged
			"server2": createTestHTTPConfig(t, ":8002"), // New
		}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 2, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Empty(t, toStop)
		assert.Contains(t, toStart, "server2")

		// Check server1 unchanged
		entry1 := entries.get("server1")
		require.NotNil(t, entry1)
		assert.Equal(t, actionNone, entry1.action)

		// Check server2 new
		entry2 := entries.get("server2")
		require.NotNil(t, entry2)
		assert.Equal(t, actionStart, entry2.action)
		assert.Nil(t, entry2.runner)
	})
}

func TestNewEntries_ServerConfigChange(t *testing.T) {
	t.Run("config change running server", func(t *testing.T) {
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					true,
				),
			},
		}

		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8002"), // Changed port
		}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 2, entries.count(), "Config change should create stop and start entries")
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Len(t, toStop, 1)

		assert.Contains(t, toStop, "server1:stop")
		stopEntry := entries.get("server1:stop")
		require.NotNil(t, stopEntry)
		assert.Equal(t, actionStop, stopEntry.action)
		assert.Equal(
			t,
			":8001",
			stopEntry.config.ListenAddr,
			"Stop entry should preserve old config",
		)

		assert.Contains(t, toStart, "server1")
		startEntry := entries.get("server1")
		require.NotNil(t, startEntry)
		assert.Equal(t, actionStart, startEntry.action)
		assert.Equal(t, ":8002", startEntry.config.ListenAddr, "Start entry should have new config")
		assert.Equal(t, "server1", startEntry.id, "Start entry should preserve original ID")
	})

	t.Run("config change non-running server", func(t *testing.T) {
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					false,
				),
			},
		}

		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8002"), // Changed port
		}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 1, entries.count(), "Non-running server config change should just update")
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Empty(t, toStop)
		assert.Contains(t, toStart, "server1")

		entry := entries.get("server1")
		require.NotNil(t, entry)
		assert.Equal(t, actionStart, entry.action)
		assert.Equal(t, ":8002", entry.config.ListenAddr, "Entry should have updated config")
	})

	t.Run("config unchanged", func(t *testing.T) {
		config := createTestHTTPConfig(t, ":8001")
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(t, "server1", config, true),
			},
		}

		configs := map[string]*httpserver.Config{
			"server1": config,
		}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(t, 1, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Empty(t, toStart)
		assert.Empty(t, toStop)

		entry := entries.get("server1")
		require.NotNil(t, entry)
		assert.Equal(t, actionNone, entry.action)
	})
}

func TestEntriesCommit(t *testing.T) {
	t.Run("commit with mixed actions", func(t *testing.T) {
		// Create entries with various actions
		entries := &entries{
			servers: map[string]*serverEntry{
				"keep": {
					id:     "keep",
					config: createTestHTTPConfig(t, ":8001"),
					action: actionNone,
				},
				"stop": {
					id:     "stop",
					config: createTestHTTPConfig(t, ":8002"),
					action: actionStop,
				},
				"start": {
					id:     "start",
					config: createTestHTTPConfig(t, ":8003"),
					action: actionStart,
				},
				"restart": {
					id:     "restart",
					config: createTestHTTPConfig(t, ":8004"),
					action: actionStart,
				},
			},
		}

		committed := entries.commit()

		assert.Equal(t, 3, committed.count(), "Stop entries should be removed after commit")

		keepEntry := committed.get("keep")
		require.NotNil(t, keepEntry, "Keep entry should remain")
		assert.Equal(t, actionNone, keepEntry.action, "Keep entry should have no action")

		assert.Nil(t, committed.get("stop"), "Stop entry should be removed")

		startEntry := committed.get("start")
		require.NotNil(t, startEntry, "Start entry should remain")
		assert.Equal(t, actionNone, startEntry.action, "Start entry action should be reset")

		restartEntry := committed.get("restart")
		require.NotNil(t, restartEntry, "Restart entry should remain")
		assert.Equal(t, "restart", restartEntry.id, "Restart entry should preserve ID")
		assert.Equal(t, actionNone, restartEntry.action, "Restart entry action should be reset")
	})
}

func TestEntriesSetRuntime(t *testing.T) {
	t.Run("set runtime for existing entry", func(t *testing.T) {
		entries := &entries{
			servers: map[string]*serverEntry{
				"server1": {
					id:     "server1",
					config: createTestHTTPConfig(t, ":8001"),
					action: actionStart,
				},
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		updated := entries.setRuntime("server1", nil, ctx, cancel)

		require.NotNil(t, updated)
		assert.Equal(t, 1, updated.count())

		entry := updated.get("server1")
		require.NotNil(t, entry)
		assert.Equal(t, ctx, entry.ctx)
		assert.Equal(t, actionStart, entry.action, "Action should be preserved")

		originalEntry := entries.get("server1")
		assert.Nil(t, originalEntry.ctx, "Original entries should be unchanged")
	})

	t.Run("set runtime for non-existing entry", func(t *testing.T) {
		entries := &entries{
			servers: map[string]*serverEntry{},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		updated := entries.setRuntime("nonexistent", nil, ctx, cancel)

		assert.Nil(t, updated)
	})
}

func TestEntriesClearRuntime(t *testing.T) {
	t.Run("clear runtime for existing entry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		entries := &entries{
			servers: map[string]*serverEntry{
				"server1": {
					id:     "server1",
					config: createTestHTTPConfig(t, ":8001"),
					ctx:    ctx,
					cancel: cancel,
					action: actionStop,
				},
			},
		}

		updated := entries.clearRuntime("server1")

		require.NotNil(t, updated)
		assert.Equal(t, 1, updated.count())

		entry := updated.get("server1")
		require.NotNil(t, entry)
		assert.Nil(t, entry.ctx)
		assert.Nil(t, entry.cancel)
		assert.Equal(t, actionStop, entry.action, "Action should be preserved")

		originalEntry := entries.get("server1")
		assert.NotNil(t, originalEntry.ctx, "Original entries should be unchanged")
	})

	t.Run("clear runtime for non-existing entry", func(t *testing.T) {
		entries := &entries{
			servers: map[string]*serverEntry{},
		}

		updated := entries.clearRuntime("nonexistent")

		assert.Nil(t, updated)
	})
}

func TestEntriesHelperMethods(t *testing.T) {
	entries := &entries{
		servers: map[string]*serverEntry{
			"start1": {action: actionStart},
			"start2": {action: actionStart},
			"stop1":  {action: actionStop},
			"none1":  {action: actionNone},
		},
	}

	t.Run("count", func(t *testing.T) {
		assert.Equal(t, 4, entries.count())
	})

	t.Run("countByAction", func(t *testing.T) {
		assert.Equal(t, 2, entries.countByAction(actionStart))
		assert.Equal(t, 1, entries.countByAction(actionStop))
		assert.Equal(t, 1, entries.countByAction(actionNone))
	})

	t.Run("getPendingActions", func(t *testing.T) {
		toStart, toStop := entries.getPendingActions()

		assert.Len(t, toStart, 2)
		assert.Len(t, toStop, 1)

		assert.Contains(t, toStart, "start1")
		assert.Contains(t, toStart, "start2")
		assert.Contains(t, toStop, "stop1")
	})

	t.Run("get", func(t *testing.T) {
		entry := entries.get("start1")
		require.NotNil(t, entry)
		assert.Equal(t, actionStart, entry.action)

		assert.Nil(t, entries.get("nonexistent"))
	})
}

func TestEntriesImmutability(t *testing.T) {
	t.Run("newEntries doesn't modify previous", func(t *testing.T) {
		original := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					true,
				),
			},
		}

		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8002"), // Different config
		}

		desiredEntries := newEntries(configs)
		newEntries := original.buildPendingEntries(desiredEntries).(*entries)

		// Original should be unchanged
		assert.Equal(t, 1, original.count())
		originalEntry := original.get("server1")
		require.NotNil(t, originalEntry)
		assert.Equal(t, actionNone, originalEntry.action)
		assert.Equal(t, ":8001", originalEntry.config.ListenAddr)

		// New entries should have changes
		assert.Equal(t, 2, newEntries.count()) // stop + start
	})

	t.Run("setRuntime doesn't modify original", func(t *testing.T) {
		original := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(
					t,
					"server1",
					createTestHTTPConfig(t, ":8001"),
					false,
				),
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		updated := original.setRuntime("server1", nil, ctx, cancel)

		// Original unchanged
		originalEntry := original.get("server1")
		assert.Nil(t, originalEntry.ctx)

		// Updated has runtime
		updatedEntry := updated.get("server1")
		assert.NotNil(t, updatedEntry.ctx)
	})
}

func TestComplexScenarios(t *testing.T) {
	t.Run("multiple changes in one update", func(t *testing.T) {
		previous := &entries{
			servers: map[string]*serverEntry{
				"keep": createTestServerEntry(t, "keep", createTestHTTPConfig(t, ":8001"), true),
				"remove": createTestServerEntry(
					t,
					"remove",
					createTestHTTPConfig(t, ":8002"),
					true,
				),
				"change": createTestServerEntry(
					t,
					"change",
					createTestHTTPConfig(t, ":8003"),
					true,
				),
			},
		}

		configs := map[string]*httpserver.Config{
			"keep":   createTestHTTPConfig(t, ":8001"),
			"change": createTestHTTPConfig(t, ":8004"),
			"add":    createTestHTTPConfig(t, ":8005"),
		}

		desiredEntries := newEntries(configs)
		entries := previous.buildPendingEntries(desiredEntries).(*entries)

		assert.Equal(
			t,
			5,
			entries.count(),
			"Should have entries for keep, remove(stop), change(stop), change(start), add(start)",
		)

		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 2, "Should start changed server and new server")
		assert.Len(t, toStop, 2, "Should stop removed server and old changed server")
		assert.Equal(t, actionNone, entries.get("keep").action)
		assert.Equal(t, actionStop, entries.get("remove").action)
		assert.Equal(t, actionStop, entries.get("change:stop").action)
		assert.Equal(t, actionStart, entries.get("change").action)
		assert.Equal(t, actionStart, entries.get("add").action)

		committed := entries.commit()
		assert.Equal(t, 3, committed.count(), "After commit should have 3 running servers")
		assert.NotNil(t, committed.get("keep"), "Keep server should remain")
		assert.NotNil(t, committed.get("change"), "Changed server should remain")
		assert.NotNil(t, committed.get("add"), "Added server should remain")
		assert.Nil(t, committed.get("remove"), "Removed server should be gone")
		assert.Nil(t, committed.get("change:stop"), "Stop entry should be removed")
	})
}

// collectEntries collects all entries from the iterator into maps for testing
func collectEntries(
	t *testing.T,
	iterator func(yield func(string, *serverEntry) bool),
) (map[string]*serverEntry, []string) {
	t.Helper()
	entries := make(map[string]*serverEntry)
	var keys []string

	for key, entry := range iterator {
		entries[key] = entry
		keys = append(keys, key)
	}

	return entries, keys
}

func TestProcessExistingServer_ServerRemoval(t *testing.T) {
	t.Parallel()
	t.Run("remove all servers with nil desired config", func(t *testing.T) {
		oldEntry := createTestServerEntry(t, "server1", createTestHTTPConfig(t, ":8001"), true)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, nil),
		)

		assert.Len(t, entries, 1)
		assert.Contains(t, keys, "server1")

		entry := entries["server1"]
		require.NotNil(t, entry)
		assert.Equal(t, actionStop, entry.action)
		assert.NotNil(t, entry.runner, "Runtime state should be preserved for stopping")
	})

	t.Run("remove non-running server", func(t *testing.T) {
		oldEntry := createTestServerEntry(t, "server1", createTestHTTPConfig(t, ":8001"), false)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, nil),
		)

		assert.Empty(t, entries)
		assert.Empty(t, keys, "Nothing should be yielded for non-running servers")
	})

	t.Run("nil old entry", func(t *testing.T) {
		entries, keys := collectEntries(
			t, processExistingServer("server1", nil, nil),
		)

		assert.Empty(t, entries)
		assert.Empty(t, keys, "Nothing should be yielded for nil entries")
	})
}

func TestProcessExistingServer_ConfigUnchanged(t *testing.T) {
	t.Parallel()
	t.Run("running server unchanged", func(t *testing.T) {
		config := createTestHTTPConfig(t, ":8001")
		oldEntry := createTestServerEntry(t, "server1", config, true)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, config),
		)

		assert.Len(t, entries, 1)
		assert.Contains(t, keys, "server1")

		entry := entries["server1"]
		require.NotNil(t, entry)
		assert.Equal(t, actionNone, entry.action)
		assert.Equal(t, config, entry.config)
		assert.NotNil(t, entry.runner, "Runtime state should be preserved")
	})

	t.Run("non-running server unchanged", func(t *testing.T) {
		config := createTestHTTPConfig(t, ":8001")
		oldEntry := createTestServerEntry(t, "server1", config, false)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, config),
		)

		assert.Len(t, entries, 1)
		assert.Contains(t, keys, "server1")

		entry := entries["server1"]
		require.NotNil(t, entry)
		assert.Equal(t, actionNone, entry.action)
		assert.Equal(t, config, entry.config)
		assert.Nil(t, entry.runner, "Non-running server should have no runtime state")
	})
}

func TestProcessExistingServer_ConfigChanged(t *testing.T) {
	t.Parallel()
	t.Run("running server config changed", func(t *testing.T) {
		oldConfig := createTestHTTPConfig(t, ":8001")
		newConfig := createTestHTTPConfig(t, ":8002")
		oldEntry := createTestServerEntry(t, "server1", oldConfig, true)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, newConfig),
		)

		assert.Len(t, entries, 2)
		assert.Contains(t, keys, "server1:stop")
		assert.Contains(t, keys, "server1")

		stopEntry := entries["server1:stop"]
		require.NotNil(t, stopEntry, "Stop entry should exist")
		assert.Equal(t, actionStop, stopEntry.action, "Stop entry should have stop action")
		assert.Equal(t, oldConfig, stopEntry.config, "Stop entry should preserve old config")
		assert.NotNil(t, stopEntry.runner, "Stop entry should have runtime state")

		startEntry := entries["server1"]
		require.NotNil(t, startEntry, "Start entry should exist")
		assert.Equal(t, actionStart, startEntry.action, "Start entry should have start action")
		assert.Equal(t, newConfig, startEntry.config, "Start entry should have new config")
		assert.Equal(t, "server1", startEntry.id, "Start entry should preserve server ID")
		assert.Nil(t, startEntry.runner, "Start entry should not have runtime state yet")
	})

	t.Run("non-running server config changed", func(t *testing.T) {
		oldConfig := createTestHTTPConfig(t, ":8001")
		newConfig := createTestHTTPConfig(t, ":8002")
		oldEntry := createTestServerEntry(t, "server1", oldConfig, false)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, newConfig),
		)

		assert.Len(t, entries, 1)
		assert.Contains(t, keys, "server1")

		entry := entries["server1"]
		require.NotNil(t, entry)
		assert.Equal(t, actionStart, entry.action)
		assert.Equal(t, newConfig, entry.config)
		assert.Equal(t, "server1", entry.id)
		assert.Nil(t, entry.runner)
	})
}

func TestProcessExistingServer_IteratorBehavior(t *testing.T) {
	t.Parallel()
	t.Run("early termination on first yield", func(t *testing.T) {
		oldConfig := createTestHTTPConfig(t, ":8001")
		newConfig := createTestHTTPConfig(t, ":8002")
		oldEntry := createTestServerEntry(t, "server1", oldConfig, true)

		iterator := processExistingServer("server1", oldEntry, newConfig)

		var yielded []string
		iterator(func(key string, entry *serverEntry) bool {
			yielded = append(yielded, key)
			return false
		})

		assert.Len(t, yielded, 1)
		assert.Equal(t, "server1:stop", yielded[0])
	})

	t.Run("collect all entries manually", func(t *testing.T) {
		oldConfig := createTestHTTPConfig(t, ":8001")
		newConfig := createTestHTTPConfig(t, ":8002")
		oldEntry := createTestServerEntry(t, "server1", oldConfig, true)

		iterator := processExistingServer("server1", oldEntry, newConfig)

		entries := make(map[string]*serverEntry)
		for key, entry := range iterator {
			entries[key] = entry
		}

		assert.Len(t, entries, 2)
		assert.Contains(t, entries, "server1:stop")
		assert.Contains(t, entries, "server1")
	})

	t.Run("range over iterator directly", func(t *testing.T) {
		config := createTestHTTPConfig(t, ":8001")
		oldEntry := createTestServerEntry(t, "server1", config, true)

		iterator := processExistingServer("server1", oldEntry, config)

		var count int
		for key, entry := range iterator {
			count++
			assert.Equal(t, "server1", key)
			assert.Equal(t, actionNone, entry.action)
		}

		assert.Equal(t, 1, count)
	})
}

func TestProcessExistingServer_EdgeCases(t *testing.T) {
	t.Parallel()
	t.Run("nil desired config vs empty config", func(t *testing.T) {
		oldEntry := createTestServerEntry(t, "server1", createTestHTTPConfig(t, ":8001"), true)

		entries1, _ := collectEntries(
			t, processExistingServer("server1", oldEntry, nil),
		)
		assert.Len(t, entries1, 1)
		assert.Equal(t, actionStop, entries1["server1"].action)
	})

	t.Run("same config object reference", func(t *testing.T) {
		config := createTestHTTPConfig(t, ":8001")
		oldEntry := createTestServerEntry(t, "server1", config, true)

		entries, _ := collectEntries(
			t, processExistingServer("server1", oldEntry, config),
		)
		assert.Len(t, entries, 1)
		assert.Equal(t, actionNone, entries["server1"].action)
	})

	t.Run("config with different content but same address", func(t *testing.T) {
		config1 := createTestHTTPConfig(t, ":8001")
		config2 := createTestHTTPConfig(t, ":8002")
		oldEntry := createTestServerEntry(t, "server1", config1, true)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, config2),
		)
		assert.Len(t, entries, 2)
		assert.Contains(t, keys, "server1:stop")
		assert.Contains(t, keys, "server1")
	})
}
