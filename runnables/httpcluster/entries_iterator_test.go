package httpcluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		assert.NotNil(t, entry.runner) // Runtime state preserved for stopping
	})

	t.Run("remove non-running server", func(t *testing.T) {
		oldEntry := createTestServerEntry(t, "server1", createTestHTTPConfig(t, ":8001"), false)

		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, nil),
		)

		assert.Len(t, entries, 0)
		assert.Len(t, keys, 0) // Nothing yielded for non-running servers
	})

	t.Run("nil old entry", func(t *testing.T) {
		entries, keys := collectEntries(
			t, processExistingServer("server1", nil, nil),
		)

		assert.Len(t, entries, 0)
		assert.Len(t, keys, 0) // Nothing yielded for nil entries
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
		assert.NotNil(t, entry.runner) // Runtime state preserved
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
		assert.Nil(t, entry.runner) // No runtime state
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

		// Check stop entry
		stopEntry := entries["server1:stop"]
		require.NotNil(t, stopEntry)
		assert.Equal(t, actionStop, stopEntry.action)
		assert.Equal(t, oldConfig, stopEntry.config) // Preserves old config
		assert.NotNil(t, stopEntry.runner)

		// Check start entry
		startEntry := entries["server1"]
		require.NotNil(t, startEntry)
		assert.Equal(t, actionStart, startEntry.action)
		assert.Equal(t, newConfig, startEntry.config) // Has new config
		assert.Equal(t, "server1", startEntry.id)
		assert.Nil(t, startEntry.runner) // No runtime state yet
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
			return false // Stop after first yield
		})

		assert.Len(t, yielded, 1)
		assert.Equal(t, "server1:stop", yielded[0])
	})

	t.Run("collect all entries manually", func(t *testing.T) {
		oldConfig := createTestHTTPConfig(t, ":8001")
		newConfig := createTestHTTPConfig(t, ":8002")
		oldEntry := createTestServerEntry(t, "server1", oldConfig, true)

		iterator := processExistingServer("server1", oldEntry, newConfig)

		// Manually collect all entries
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

		// nil config should trigger removal
		entries1, _ := collectEntries(
			t, processExistingServer("server1", oldEntry, nil),
		)
		assert.Len(t, entries1, 1)
		assert.Equal(t, actionStop, entries1["server1"].action)
	})

	t.Run("same config object reference", func(t *testing.T) {
		config := createTestHTTPConfig(t, ":8001")
		oldEntry := createTestServerEntry(t, "server1", config, true)

		// Same config reference should be detected as unchanged
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

		// Different configs should trigger restart
		entries, keys := collectEntries(
			t, processExistingServer("server1", oldEntry, config2),
		)
		assert.Len(t, entries, 2)
		assert.Contains(t, keys, "server1:stop")
		assert.Contains(t, keys, "server1")
	})
}
