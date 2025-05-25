package httpcluster

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLogger is a shared test logger that discards output
var testLogger = slog.New(
	slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}),
)

// createTestHTTPConfig creates a test httpserver config
func createTestHTTPConfig(t *testing.T, addr string) *httpserver.Config {
	t.Helper()
	route, err := httpserver.NewRoute("test", "/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
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
		entry.stateSub = make(<-chan string)
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

		entries := newEntries(nil, configs, testLogger)

		assert.Equal(t, 2, entries.count())

		// Both servers should be marked for start
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 2)
		assert.Len(t, toStop, 0)
		assert.Contains(t, toStart, "server1")
		assert.Contains(t, toStart, "server2")

		// Check individual entries
		entry1 := entries.get("server1")
		require.NotNil(t, entry1)
		assert.Equal(t, "server1", entry1.id)
		assert.Equal(t, actionStart, entry1.action)
		assert.Nil(t, entry1.runner)
	})

	t.Run("empty previous state", func(t *testing.T) {
		previous := &entries{servers: make(map[string]*serverEntry)}
		configs := map[string]*httpserver.Config{
			"server1": createTestHTTPConfig(t, ":8001"),
		}

		entries := newEntries(previous, configs, testLogger)

		assert.Equal(t, 1, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Len(t, toStop, 0)
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

		entries := newEntries(previous, configs, testLogger)

		assert.Equal(t, 1, entries.count()) // Server marked for stop
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 0)
		assert.Len(t, toStop, 1)
		assert.Contains(t, toStop, "server1")

		entry := entries.get("server1")
		require.NotNil(t, entry)
		assert.Equal(t, actionStop, entry.action)
		assert.NotNil(t, entry.runner) // Runtime state preserved for stopping
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

		entries := newEntries(previous, configs, testLogger)

		assert.Equal(t, 0, entries.count()) // Server not copied (no runtime to stop)
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 0)
		assert.Len(t, toStop, 0)
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

		entries := newEntries(previous, configs, testLogger)

		assert.Equal(t, 1, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 0)
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

		entries := newEntries(previous, configs, testLogger)

		assert.Equal(t, 2, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Len(t, toStop, 0)
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

		entries := newEntries(previous, configs, testLogger)

		// Should have both stop and start entries
		assert.Equal(t, 2, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Len(t, toStop, 1)

		// Check stop entry (with :stop suffix)
		assert.Contains(t, toStop, "server1:stop")
		stopEntry := entries.get("server1:stop")
		require.NotNil(t, stopEntry)
		assert.Equal(t, actionStop, stopEntry.action)
		assert.Equal(t, ":8001", stopEntry.config.ListenAddr) // Old config preserved

		// Check start entry (original id)
		assert.Contains(t, toStart, "server1")
		startEntry := entries.get("server1")
		require.NotNil(t, startEntry)
		assert.Equal(t, actionStart, startEntry.action)
		assert.Equal(t, ":8002", startEntry.config.ListenAddr) // New config
		assert.Equal(t, "server1", startEntry.id)              // Original ID preserved
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

		entries := newEntries(previous, configs, testLogger)

		// Should just update config and mark for start
		assert.Equal(t, 1, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 1)
		assert.Len(t, toStop, 0)
		assert.Contains(t, toStart, "server1")

		entry := entries.get("server1")
		require.NotNil(t, entry)
		assert.Equal(t, actionStart, entry.action)
		assert.Equal(t, ":8002", entry.config.ListenAddr) // New config
	})

	t.Run("config unchanged", func(t *testing.T) {
		config := createTestHTTPConfig(t, ":8001")
		previous := &entries{
			servers: map[string]*serverEntry{
				"server1": createTestServerEntry(t, "server1", config, true),
			},
		}

		configs := map[string]*httpserver.Config{
			"server1": config, // Same config object
		}

		entries := newEntries(previous, configs, testLogger)

		assert.Equal(t, 1, entries.count())
		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 0)
		assert.Len(t, toStop, 0)

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
				"restart:new": {
					id:     "restart",
					config: createTestHTTPConfig(t, ":8004"),
					action: actionStart,
				},
			},
		}

		committed := entries.commit()

		// Should have 3 servers (stop entry removed, :new suffix removed)
		assert.Equal(t, 3, committed.count())

		// Check kept entry
		keepEntry := committed.get("keep")
		require.NotNil(t, keepEntry)
		assert.Equal(t, actionNone, keepEntry.action)

		// Check stopped entry is gone
		assert.Nil(t, committed.get("stop"))

		// Check started entry
		startEntry := committed.get("start")
		require.NotNil(t, startEntry)
		assert.Equal(t, actionNone, startEntry.action)

		// Check restarted entry (suffix removed)
		restartEntry := committed.get("restart")
		require.NotNil(t, restartEntry)
		assert.Equal(t, "restart", restartEntry.id)
		assert.Equal(t, actionNone, restartEntry.action)
		assert.Nil(t, committed.get("restart:new"))
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
		stateSub := make(<-chan string)

		updated := entries.setRuntime("server1", nil, ctx, cancel, stateSub)

		require.NotNil(t, updated)
		assert.Equal(t, 1, updated.count())

		entry := updated.get("server1")
		require.NotNil(t, entry)
		assert.Equal(t, ctx, entry.ctx)
		assert.Equal(t, stateSub, entry.stateSub)
		assert.Equal(t, actionStart, entry.action) // Action preserved

		// Original entries unchanged
		originalEntry := entries.get("server1")
		assert.Nil(t, originalEntry.ctx)
	})

	t.Run("set runtime for non-existing entry", func(t *testing.T) {
		entries := &entries{
			servers: map[string]*serverEntry{},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		updated := entries.setRuntime("nonexistent", nil, ctx, cancel, nil)

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
		assert.Equal(t, actionStop, entry.action) // Action preserved

		// Original entries unchanged
		originalEntry := entries.get("server1")
		assert.NotNil(t, originalEntry.ctx)
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

		newEntries := newEntries(original, configs, testLogger)

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

		updated := original.setRuntime("server1", nil, ctx, cancel, nil)

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
		// Previous state: 3 servers running
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

		// New config: keep one, remove one, change one, add one
		configs := map[string]*httpserver.Config{
			"keep":   createTestHTTPConfig(t, ":8001"), // Unchanged
			"change": createTestHTTPConfig(t, ":8004"), // Changed port
			"add":    createTestHTTPConfig(t, ":8005"), // New server
			// "remove" not in new config
		}

		entries := newEntries(previous, configs, testLogger)

		// Should have 5 entries: keep(none) + remove(stop) + change(stop) + change:new(start) + add(start)
		assert.Equal(t, 5, entries.count())

		toStart, toStop := entries.getPendingActions()
		assert.Len(t, toStart, 2) // change:new + add
		assert.Len(t, toStop, 2)  // remove + change

		// Verify specific entries
		assert.Equal(t, actionNone, entries.get("keep").action)
		assert.Equal(t, actionStop, entries.get("remove").action)
		assert.Equal(t, actionStop, entries.get("change:stop").action)
		assert.Equal(t, actionStart, entries.get("change").action)
		assert.Equal(t, actionStart, entries.get("add").action)

		// After commit, should have 3 servers
		committed := entries.commit()
		assert.Equal(t, 3, committed.count())
		assert.NotNil(t, committed.get("keep"))
		assert.NotNil(t, committed.get("change"))
		assert.NotNil(t, committed.get("add"))
		assert.Nil(t, committed.get("remove"))
		assert.Nil(t, committed.get("change:stop"))
	})
}
