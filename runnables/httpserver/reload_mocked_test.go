package httpserver

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a test Config with a single route
func createTestConfigForReload(
	t *testing.T,
	addr string,
	path string,
	drainTimeout time.Duration,
) *Config {
	t.Helper()
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	route, err := NewRouteFromHandlerFunc("test", path, handler)
	require.NoError(t, err)
	cfg, err := NewConfig(addr, Routes{*route}, WithDrainTimeout(drainTimeout))
	require.NoError(t, err)
	return cfg
}

// TestReloadConfig_ConfigEquality tests that equal configs are detected properly
func TestReloadConfig_ConfigEquality(t *testing.T) {
	t.Parallel()

	// Create two configs that are logically equivalent but different instances
	config1 := createTestConfigForReload(t, ":8500", "/", 1*time.Second)
	config2 := createTestConfigForReload(t, ":8500", "/", 1*time.Second)

	// Verify they're not the same instance but are logically equal
	assert.NotSame(t, config1, config2)
	assert.True(t, config1.Equal(config2))

	callCount := 0
	configCallback := func() (*Config, error) {
		callCount++
		if callCount == 1 {
			return config1, nil
		}
		return config2, nil
	}

	// Create runner with dynamic callback
	runner, err := NewRunner(WithConfigCallback(configCallback))
	require.NoError(t, err)

	// First load should succeed
	assert.Same(t, config1, runner.getConfig())

	// Second reload should return ErrOldConfig since configs are equal
	err = runner.reloadConfig()
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrOldConfig)

	// Config should not have changed
	assert.Same(t, config1, runner.getConfig())
}

// TestReloadConfig_ConfigChanges tests sequential config changes
func TestReloadConfig_ConfigChanges(t *testing.T) {
	t.Parallel()

	config1 := createTestConfigForReload(t, ":8400", "/", 1*time.Second)
	config2 := createTestConfigForReload(t, ":8400", "/modified", 1*time.Second) // Different path
	config3 := createTestConfigForReload(t, ":8401", "/", 1*time.Second)         // Different port

	configs := []*Config{config1, config2, config3}
	callCount := 0
	configCallback := func() (*Config, error) {
		cfg := configs[callCount]
		callCount++
		return cfg, nil
	}

	// Create runner
	runner, err := NewRunner(WithConfigCallback(configCallback))
	require.NoError(t, err)

	// Initial config should be config1
	assert.Same(t, config1, runner.getConfig())

	// Reload to config2 should succeed
	err = runner.reloadConfig()
	assert.NoError(t, err)
	assert.Same(t, config2, runner.getConfig())

	// Reload to config3 should succeed
	err = runner.reloadConfig()
	assert.NoError(t, err)
	assert.Same(t, config3, runner.getConfig())
}

// TestReloadConfig_WithFullRunner tests the reloadConfig method with a real Runner
func TestReloadConfig_WithFullRunner(t *testing.T) {
	t.Parallel()

	// Test using a real Runner instance
	t.Run("Full Runner with dynamic config callback", func(t *testing.T) {
		// Skip if integration tests are disabled
		if testing.Short() {
			t.Skip("Skipping integration test in short mode")
		}

		// Create initial config
		listenPort := fmt.Sprintf(":%d", networking.GetRandomPort(t))
		initialConfig := createTestConfigForReload(t, listenPort, "/", 1*time.Second)
		modifiedConfig := createTestConfigForReload(t, listenPort, "/modified", 2*time.Second)

		// Create a dynamically changing config callback
		currentConfig := initialConfig
		configCallback := func() (*Config, error) {
			return currentConfig, nil
		}

		// Create the Runner with the config callback
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Verify initial config is set
		config := runner.getConfig()
		assert.NotNil(t, config)
		assert.Equal(t, listenPort, config.ListenAddr)
		assert.Equal(t, 1*time.Second, config.DrainTimeout)

		// Change current config to modifiedConfig
		currentConfig = modifiedConfig

		// Reload config
		err = runner.reloadConfig()
		assert.NoError(t, err)

		// Verify config was updated
		updatedConfig := runner.getConfig()
		assert.NotNil(t, updatedConfig)
		assert.Equal(t, listenPort, updatedConfig.ListenAddr)
		assert.Equal(t, 2*time.Second, updatedConfig.DrainTimeout)
		assert.Len(t, updatedConfig.Routes, 1)
		assert.Equal(t, "/modified", updatedConfig.Routes[0].Path)
	})
}

// TestReloadConfig_ErrorCases tests error handling in reloadConfig
func TestReloadConfig_ErrorCases(t *testing.T) {
	t.Parallel()

	// Test case: Error when configCallback returns an error
	t.Run("Error when configCallback returns an error", func(t *testing.T) {
		t.Parallel()

		// Create a callback that returns an error
		expectedErr := errors.New("config callback error")
		configCallback := func() (*Config, error) {
			return nil, expectedErr
		}

		// Create real runner - will fail during NewRunner due to error
		_, err := NewRunner(WithConfigCallback(configCallback))
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrConfigCallback)
	})

	// Test case: Error when configCallback returns a nil config
	t.Run("Error when configCallback returns a nil config", func(t *testing.T) {
		t.Parallel()

		// Create a callback that returns nil config
		configCallback := func() (*Config, error) {
			return nil, nil
		}

		// Create real runner - will fail during NewRunner due to nil config
		_, err := NewRunner(WithConfigCallback(configCallback))
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrConfigCallback)
	})

	// Test case: Success when oldConfig is nil (initial config load)
	t.Run("Success when oldConfig is nil (initial config load)", func(t *testing.T) {
		t.Parallel()

		// Create test config
		testConfig := createTestConfigForReload(t, ":8100", "/", 1*time.Second)
		configCallback := func() (*Config, error) {
			return testConfig, nil
		}

		// Create real runner with valid config
		runner, err := NewRunner(WithConfigCallback(configCallback))
		require.NoError(t, err)

		// Verify config was set during initialization
		assert.NotNil(t, runner.getConfig())
		assert.Same(t, testConfig, runner.getConfig())
	})
}
