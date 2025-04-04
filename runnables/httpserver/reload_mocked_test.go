package httpserver

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockConfigCallback is a mock implementation of the config callback function
type MockConfigCallback struct {
	mock.Mock
}

// Call implements the config callback function
func (m *MockConfigCallback) Call() (*Config, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*Config), args.Error(1)
}

// Helper function to create a test route
func createTestRouteForMock(t *testing.T, path string) Route {
	t.Helper()
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	route, err := NewRoute("test", path, handler)
	require.NoError(t, err)
	return *route
}

// Helper function to create a simple test config
func createSimpleConfigForMock(t *testing.T, addr string) *Config {
	t.Helper()
	routes := Routes{createTestRouteForMock(t, "/")}
	cfg, err := NewConfig(addr, 1*time.Second, routes)
	require.NoError(t, err)
	return cfg
}

// Helper function to create a modified config
func createModifiedConfigForMock(t *testing.T, addr string) *Config {
	t.Helper()
	routes := Routes{createTestRouteForMock(t, "/modified")}
	cfg, err := NewConfig(addr, 2*time.Second, routes)
	require.NoError(t, err)
	return cfg
}

// TestReloadConfig_WithMock tests the reloadConfig method using mocks
func TestReloadConfig_WithMock(t *testing.T) {
	t.Parallel()

	// Test case 1: Error when configCallback returns an error
	t.Run("Error when configCallback returns an error", func(t *testing.T) {
		t.Parallel()

		// Setup mock callback
		mockCallback := new(MockConfigCallback)
		expectedErr := errors.New("config callback error")
		mockCallback.On("Call").Return(nil, expectedErr)

		// Create Runner with the mock callback
		runner := &Runner{
			configCallback: mockCallback.Call,
			fsm:            NewMockStateMachine(),
		}

		// Call reloadConfig
		err := runner.reloadConfig()

		// Verify expectations
		mockCallback.AssertExpectations(t)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to reload config")
		assert.ErrorIs(t, err, expectedErr)
	})

	// Test case 2: Error when configCallback returns a nil config
	t.Run("Error when configCallback returns a nil config", func(t *testing.T) {
		t.Parallel()

		// Setup mock callback
		mockCallback := new(MockConfigCallback)
		mockCallback.On("Call").Return(nil, nil)

		// Create Runner with the mock callback
		runner := &Runner{
			configCallback: mockCallback.Call,
			fsm:            NewMockStateMachine(),
		}

		// Call reloadConfig
		err := runner.reloadConfig()

		// Verify expectations
		mockCallback.AssertExpectations(t)
		assert.Error(t, err)
		assert.Equal(t, "config callback returned nil", err.Error())
	})

	// Test case 3: Success when oldConfig is nil (initial config load)
	t.Run("Success when oldConfig is nil (initial config load)", func(t *testing.T) {
		t.Parallel()

		// Create test config
		testConfig := createSimpleConfigForMock(t, ":8100")

		// Setup mock callback
		mockCallback := new(MockConfigCallback)
		mockCallback.On("Call").Return(testConfig, nil)

		// Create MockRunner with the mock callback
		mockRunner := NewMockRunner(mockCallback.Call, NewMockStateMachine())

		// Call reloadConfig
		err := mockRunner.reloadConfig()

		// Verify expectations
		mockCallback.AssertExpectations(t)
		assert.NoError(t, err)

		// Verify config was set
		assert.NotNil(t, mockRunner.storedConfig)
		assert.Same(t, testConfig, mockRunner.storedConfig)
	})

	// Test case 4: ErrOldConfig returned when the new config matches the old config
	t.Run("ErrOldConfig returned when new config matches old config", func(t *testing.T) {
		t.Parallel()

		// Create test config
		testConfig := createSimpleConfigForMock(t, ":8200")

		// Setup mock callback that returns the same config
		mockCallback := new(MockConfigCallback)
		mockCallback.On("Call").Return(testConfig, nil)

		// Create MockRunner with the mock callback
		mockRunner := NewMockRunner(mockCallback.Call, NewMockStateMachine())

		// Pre-set the config to simulate existing config
		mockRunner.storedConfig = testConfig

		// Call reloadConfig
		err := mockRunner.reloadConfig()

		// Verify expectations
		mockCallback.AssertExpectations(t)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOldConfig)

		// Verify config remained the same
		assert.Same(t, testConfig, mockRunner.storedConfig)
	})

	// Test case 5: Success when oldConfig is different from newConfig
	t.Run("Success when oldConfig is different from newConfig", func(t *testing.T) {
		t.Parallel()

		// Create old and new configs
		oldConfig := createSimpleConfigForMock(t, ":8300")
		newConfig := createModifiedConfigForMock(t, ":8301")

		// Setup mock callback
		mockCallback := new(MockConfigCallback)
		mockCallback.On("Call").Return(newConfig, nil)

		// Create MockRunner with the mock callback
		mockRunner := NewMockRunner(mockCallback.Call, NewMockStateMachine())

		// Pre-set the old config
		mockRunner.storedConfig = oldConfig

		// Call reloadConfig
		err := mockRunner.reloadConfig()

		// Verify expectations
		mockCallback.AssertExpectations(t)
		assert.NoError(t, err)

		// Verify config was updated
		assert.NotNil(t, mockRunner.storedConfig)
		assert.Same(t, newConfig, mockRunner.storedConfig)
		assert.NotSame(t, oldConfig, mockRunner.storedConfig)
	})
}

// TestReloadConfig_EdgeCases tests edge cases in the reloadConfig method
func TestReloadConfig_EdgeCases(t *testing.T) {
	t.Parallel()

	// Test with sequential configs
	t.Run("Sequential config changes", func(t *testing.T) {
		t.Parallel()

		// Create a set of configs for testing
		config1 := createSimpleConfigForMock(t, ":8400")
		config2 := createModifiedConfigForMock(t, ":8400") // Same port, different routes
		config3 := createSimpleConfigForMock(t, ":8401")   // Different port

		// Create a mock FSM
		mockFSM := NewMockStateMachine()
		mockFSM.On("GetState").Return(finitestate.StatusRunning)

		// Create a sequence of callbacks
		mockCallback1 := new(MockConfigCallback)
		mockCallback1.On("Call").Return(config1, nil)

		mockCallback2 := new(MockConfigCallback)
		mockCallback2.On("Call").Return(config2, nil)

		mockCallback3 := new(MockConfigCallback)
		mockCallback3.On("Call").Return(config3, nil)

		// Create MockRunner with the first mock callback
		mockRunner := NewMockRunner(mockCallback1.Call, mockFSM)

		// First call should succeed (no previous config)
		err := mockRunner.reloadConfig()
		assert.NoError(t, err)
		assert.Same(t, config1, mockRunner.storedConfig)
		mockCallback1.AssertExpectations(t)

		// Change callback to second mock
		mockRunner.callback = mockCallback2.Call

		// Second call should succeed (different config)
		err = mockRunner.reloadConfig()
		assert.NoError(t, err)
		assert.Same(t, config2, mockRunner.storedConfig)
		mockCallback2.AssertExpectations(t)

		// Change callback to third mock
		mockRunner.callback = mockCallback3.Call

		// Third call should succeed (different config)
		err = mockRunner.reloadConfig()
		assert.NoError(t, err)
		assert.Same(t, config3, mockRunner.storedConfig)
		mockCallback3.AssertExpectations(t)
	})

	// Test with logically equivalent configs
	t.Run("Logically equivalent configs should be detected", func(t *testing.T) {
		t.Parallel()

		// Create two configs that are logically equivalent but different instances
		config1 := createSimpleConfigForMock(t, ":8500")

		// Create config2 that has the same values as config1
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}
		route, err := NewRoute("test", "/", handler)
		require.NoError(t, err)
		config2, err := NewConfig(":8500", 1*time.Second, Routes{*route})
		require.NoError(t, err)

		// Verify they're not the same instance but are logically equal
		assert.NotSame(t, config1, config2)
		assert.True(t, config1.Equal(config2))

		// Setup mock callbacks
		mockCallback1 := new(MockConfigCallback)
		mockCallback1.On("Call").Return(config1, nil)

		mockCallback2 := new(MockConfigCallback)
		mockCallback2.On("Call").Return(config2, nil)

		// Create MockRunner with the first mock callback
		mockRunner := NewMockRunner(mockCallback1.Call, NewMockStateMachine())

		// First call should succeed (no previous config)
		err = mockRunner.reloadConfig()
		assert.NoError(t, err)
		assert.Same(t, config1, mockRunner.storedConfig)
		mockCallback1.AssertExpectations(t)

		// Change callback to second mock
		mockRunner.callback = mockCallback2.Call

		// Second call should return ErrOldConfig since configs are logically equal
		err = mockRunner.reloadConfig()
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrOldConfig)
		mockCallback2.AssertExpectations(t)

		// Config should not have changed
		assert.Same(t, config1, mockRunner.storedConfig)
	})
}

// TestReloadConfig_WithFullRunner tests the reloadConfig method with a real Runner
func TestReloadConfig_WithFullRunner(t *testing.T) {
	t.Parallel()

	// Test using a real Runner instance
	t.Run("Full Runner with mocked config callback", func(t *testing.T) {
		// Skip if integration tests are disabled
		if testing.Short() {
			t.Skip("Skipping integration test in short mode")
		}

		// Create initial config
		listenPort := getAvailablePort(t, 8600)
		handler := func(w http.ResponseWriter, r *http.Request) {}
		route, err := NewRoute("v1", "/", handler)
		require.NoError(t, err)
		initialRoutes := Routes{*route}

		initialConfig, err := NewConfig(listenPort, 1*time.Second, initialRoutes)
		require.NoError(t, err)

		// Create a modified config for the reload
		modifiedHandler := func(w http.ResponseWriter, r *http.Request) {}
		modifiedRoute, err := NewRoute("v2", "/modified", modifiedHandler)
		require.NoError(t, err)
		modifiedRoutes := Routes{*modifiedRoute}

		modifiedConfig, err := NewConfig(listenPort, 2*time.Second, modifiedRoutes)
		require.NoError(t, err)

		// Create a dynamically changing config callback
		currentConfig := initialConfig
		configCallback := func() (*Config, error) {
			return currentConfig, nil
		}

		// Create the Runner with the config callback
		runner, err := NewRunner(
			WithContext(context.Background()),
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
