/*
Package mocks_test provides tests to ensure mocks correctly implement all supervisor interfaces.
*/
package mocks_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestInterfaceGuards uses compile-time type assertions to ensure that mock implementations
// correctly implement their respective interfaces.
func TestInterfaceGuards(t *testing.T) {
	// Create mock instances
	mockRunnable := mocks.NewMockRunnable()
	mockRunnableWithState := mocks.NewMockRunnableWithStateable()
	mockRunnableWithReload := mocks.NewMockRunnableWithReloadSender()
	mockRunnableWithShutdown := mocks.NewMockRunnableWithShutdownSender()

	// Type assertions to verify mock types implement the correct interfaces
	var (
		// Basic Runnable should implement the base Runnable interface
		_ supervisor.Runnable   = mockRunnable
		_ supervisor.Reloadable = mockRunnable

		// MockRunnableWithState should implement the base Runnable interface + Stateable
		_ supervisor.Runnable   = mockRunnableWithState
		_ supervisor.Reloadable = mockRunnableWithState
		_ supervisor.Stateable  = mockRunnableWithState

		// MockRunnableWithReload should implement the base Runnable interface + ReloadSender
		_ supervisor.Runnable     = mockRunnableWithReload
		_ supervisor.Reloadable   = mockRunnableWithReload
		_ supervisor.ReloadSender = mockRunnableWithReload

		// MockRunnableWithReload should implement the base Runnable interface + ReloadSender
		_ supervisor.Runnable       = mockRunnableWithShutdown
		_ supervisor.Reloadable     = mockRunnableWithShutdown
		_ supervisor.ShutdownSender = mockRunnableWithShutdown
	)
}

// testHelperInheritedMethods checks that a mock properly inherits all base Runnable behavior
func testHelperInheritedMethods(t *testing.T, mockRunnable any, displayName string) {
	t.Helper()

	// Add expectations based on the type of mock
	switch mockObj := mockRunnable.(type) {
	case *mocks.Runnable:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload", mock.Anything).Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		require.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload(t.Context())
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	case *mocks.MockRunnableWithStateable:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload", mock.Anything).Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		require.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload(t.Context())
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	case *mocks.MockRunnableWithReloadSender:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload", mock.Anything).Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		require.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload(t.Context())
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	case *mocks.MockRunnableWithShutdownSender:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload", mock.Anything).Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		require.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload(t.Context())
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	case *mocks.MockRunnableWithReadiness:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload", mock.Anything).Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		require.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload(t.Context())
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	default:
		t.Fatalf("Unsupported mock type: %T", mockRunnable)
	}
}

func TestMockRunnable(t *testing.T) {
	t.Run("Run method", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnable()

		expectedErr := errors.New("test error")
		mockRunnable.On("Run", mock.Anything).Return(expectedErr)

		err := mockRunnable.Run(context.Background())

		assert.Equal(t, expectedErr, err)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("Stop method", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("Stop").Return()

		mockRunnable.Stop()

		mockRunnable.AssertExpectations(t)
	})

	t.Run("Reload method", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("Reload", mock.Anything).Return()

		mockRunnable.Reload(t.Context())

		mockRunnable.AssertExpectations(t)
	})

	t.Run("String method with mock expectation", func(t *testing.T) {
		// Create a mock
		mockRunnable := mocks.NewMockRunnable()

		// Set up expectations
		mockRunnable.On("String").Return("CustomRunnable")

		// Call the method
		result := mockRunnable.String()

		// Verify behavior
		assert.Equal(t, "CustomRunnable", result)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("String method default value", func(t *testing.T) {
		// Create a mock
		mockRunnable := mocks.NewMockRunnable()

		// Set up expectations to return nil
		mockRunnable.On("String").Return(nil)

		// Call the method
		result := mockRunnable.String()

		// Verify default is returned
		assert.Equal(t, "Runnable", result)
		mockRunnable.AssertExpectations(t)
	})
}

func TestMockRunnableWithStateable(t *testing.T) {
	t.Run("GetState method", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnableWithStateable()
		mockRunnable.On("GetState").Return("running")

		state := mockRunnable.GetState()

		assert.Equal(t, "running", state)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("GetStateChan method", func(t *testing.T) {
		// Create a mock
		mockRunnable := mocks.NewMockRunnableWithStateable()

		// Create a test channel
		testChan := make(chan string, 1)
		testChan <- "running"

		// Set up expectations
		mockRunnable.On("GetStateChan", mock.Anything).Return(testChan)

		// Call the method
		resultChan := mockRunnable.GetStateChan(context.Background())

		// Verify behavior - we should receive the state from the channel
		state := <-resultChan
		assert.Equal(t, "running", state)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("IsReady method", func(t *testing.T) {
		// Create a mock
		mockRunnable := mocks.NewMockRunnableWithStateable()
		// IsReady takes no arguments; the expectation must match accordingly.
		mockRunnable.On("IsReady").Return(true)
		r := mockRunnable.IsReady()
		assert.True(t, r)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("inherits from base Runnable", func(t *testing.T) {
		testHelperInheritedMethods(t, mocks.NewMockRunnableWithStateable(), "StatefulService")
	})
}

func TestMockRunnableWithReadiness(t *testing.T) {
	t.Run("IsReady method", func(t *testing.T) {
		mockRunnable := mocks.NewMockRunnableWithReadiness()
		mockRunnable.On("IsReady").Return(true).Once()
		require.True(t, mockRunnable.IsReady())
		mockRunnable.AssertExpectations(t)
	})

	t.Run("inherits from base Runnable", func(t *testing.T) {
		testHelperInheritedMethods(t, mocks.NewMockRunnableWithReadiness(), "ReadinessService")
	})
}

func TestMockRunnableWithReloadSender(t *testing.T) {
	t.Run("GetReloadTrigger method", func(t *testing.T) {
		// Create a mock
		mockRunnable := mocks.NewMockRunnableWithReloadSender()

		// Create a test channel
		testChan := make(chan struct{}, 1)
		testChan <- struct{}{}

		// Set up expectations
		mockRunnable.On("GetReloadTrigger").Return(testChan)

		// Call the method
		resultChan := mockRunnable.GetReloadTrigger()

		// Verify behavior - we should receive a signal from the channel
		select {
		case <-resultChan:
			// Success - we received a signal
		case <-time.After(50 * time.Millisecond):
			t.Fatal("Did not receive expected signal from reload channel")
		}

		mockRunnable.AssertExpectations(t)
	})

	t.Run("inherits from base Runnable", func(t *testing.T) {
		testHelperInheritedMethods(t, mocks.NewMockRunnableWithReloadSender(), "ReloadingService")
	})
}

func TestMockRunnableWithShutdownSender(t *testing.T) {
	t.Run("GetShutdownTrigger method", func(t *testing.T) {
		// Create a mock
		mockRunnable := mocks.NewMockRunnableWithShutdownSender()

		// Create a test channel
		testChan := make(chan struct{}, 1)
		testChan <- struct{}{}

		// Set up expectations
		mockRunnable.On("GetShutdownTrigger").Return(testChan)

		// Call the method
		resultChan := mockRunnable.GetShutdownTrigger()

		// Verify behavior - we should receive a signal from the channel
		select {
		case <-resultChan:
			// Success - we received a signal
		case <-time.After(50 * time.Millisecond):
			t.Fatal("Did not receive expected signal from shutdown channel")
		}

		mockRunnable.AssertExpectations(t)
	})

	t.Run("inherits from base Runnable", func(t *testing.T) {
		testHelperInheritedMethods(t, mocks.NewMockRunnableWithShutdownSender(), "ShutdownService")
	})
}

// TestFactoryMethods tests the constructor functions for all mock types
func TestFactoryMethods(t *testing.T) {
	t.Run("NewMockRunnable", func(t *testing.T) {
		m := mocks.NewMockRunnable()
		assert.NotNil(t, m)
	})

	t.Run("NewMockRunnableWithStateable", func(t *testing.T) {
		m := mocks.NewMockRunnableWithStateable()
		assert.NotNil(t, m)
	})

	t.Run("NewMockRunnableWithReloadSender", func(t *testing.T) {
		m := mocks.NewMockRunnableWithReloadSender()
		assert.NotNil(t, m)
	})

	t.Run("NewMockRunnableWithShutdownSender", func(t *testing.T) {
		m := mocks.NewMockRunnableWithShutdownSender()
		assert.NotNil(t, m)
	})
}
