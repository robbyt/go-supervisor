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
)

// TestInterfaceGuards uses compile-time type assertions to ensure that mock implementations
// correctly implement their respective interfaces.
func TestInterfaceGuards(t *testing.T) {
	// Create mock instances
	mockRunnable := mocks.NewMockRunnable()
	mockRunnableWithState := mocks.NewMockRunnableWithStatable()
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
func testHelperInheritedMethods(t *testing.T, mockRunnable interface{}, displayName string) {
	t.Helper()

	// Add expectations based on the type of mock
	switch mockObj := mockRunnable.(type) {
	case *mocks.Runnable:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload").Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		assert.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload()
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	case *mocks.MockRunnableWithStatable:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload").Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		assert.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload()
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	case *mocks.MockRunnableWithReloadSender:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload").Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		assert.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload()
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	case *mocks.MockRunnableWithShutdownSender:
		mockObj.On("Run", mock.Anything).Return(nil)
		mockObj.On("Stop").Return()
		mockObj.On("Reload").Return()
		mockObj.On("String").Return(displayName)

		// Call and verify methods
		err := mockObj.Run(context.Background())
		assert.NoError(t, err)
		mockObj.Stop()
		mockObj.Reload()
		name := mockObj.String()
		assert.Equal(t, displayName, name)
		mockObj.AssertExpectations(t)

	default:
		t.Fatalf("Unsupported mock type: %T", mockRunnable)
	}
}

func TestMockRunnable(t *testing.T) {
	t.Run("Run method", func(t *testing.T) {
		// Create a mock with a shorter delay for testing
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.DelayRun = 5 * time.Millisecond

		// Set up expectations
		expectedErr := errors.New("test error")
		mockRunnable.On("Run", mock.Anything).Return(expectedErr)

		// Call the method
		start := time.Now()
		err := mockRunnable.Run(context.Background())
		elapsed := time.Since(start)

		// Verify behavior
		assert.Equal(t, expectedErr, err)
		assert.GreaterOrEqual(t, elapsed, 5*time.Millisecond)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("Stop method", func(t *testing.T) {
		// Create a mock with a shorter delay for testing
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.DelayStop = 5 * time.Millisecond

		// Set up expectations
		mockRunnable.On("Stop").Return()

		// Call the method
		start := time.Now()
		mockRunnable.Stop()
		elapsed := time.Since(start)

		// Verify behavior
		assert.GreaterOrEqual(t, elapsed, 5*time.Millisecond)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("Reload method", func(t *testing.T) {
		// Create a mock with a shorter delay for testing
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.DelayReload = 5 * time.Millisecond

		// Set up expectations
		mockRunnable.On("Reload").Return()

		// Call the method
		start := time.Now()
		mockRunnable.Reload()
		elapsed := time.Since(start)

		// Verify behavior
		assert.GreaterOrEqual(t, elapsed, 5*time.Millisecond)
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

func TestMockRunnableWithStatable(t *testing.T) {
	t.Run("GetState method", func(t *testing.T) {
		// Create a mock with a shorter delay for testing
		mockRunnable := mocks.NewMockRunnableWithStatable()
		mockRunnable.DelayGetState = 5 * time.Millisecond

		// Set up expectations
		mockRunnable.On("GetState").Return("running")

		// Call the method
		start := time.Now()
		state := mockRunnable.GetState()
		elapsed := time.Since(start)

		// Verify behavior
		assert.Equal(t, "running", state)
		assert.GreaterOrEqual(t, elapsed, 5*time.Millisecond)
		mockRunnable.AssertExpectations(t)
	})

	t.Run("GetStateChan method", func(t *testing.T) {
		// Create a mock
		mockRunnable := mocks.NewMockRunnableWithStatable()

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

	t.Run("inherits from base Runnable", func(t *testing.T) {
		testHelperInheritedMethods(t, mocks.NewMockRunnableWithStatable(), "StatefulService")
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
	t.Run("NewMockRunnable creates with default delays", func(t *testing.T) {
		mock := mocks.NewMockRunnable()
		assert.Equal(t, 1*time.Millisecond, mock.DelayRun)
		assert.Equal(t, 1*time.Millisecond, mock.DelayStop)
		assert.Equal(t, 1*time.Millisecond, mock.DelayReload)
	})

	t.Run("NewMockRunnableWithStatable creates with default delays", func(t *testing.T) {
		mock := mocks.NewMockRunnableWithStatable()
		assert.Equal(t, 1*time.Millisecond, mock.DelayRun)
		assert.Equal(t, 1*time.Millisecond, mock.DelayStop)
		assert.Equal(t, 1*time.Millisecond, mock.DelayReload)
		assert.Equal(t, 1*time.Millisecond, mock.DelayGetState)
	})

	t.Run("NewMockRunnableWithReloadSender creates with default delays", func(t *testing.T) {
		mock := mocks.NewMockRunnableWithReloadSender()
		assert.Equal(t, 1*time.Millisecond, mock.DelayRun)
		assert.Equal(t, 1*time.Millisecond, mock.DelayStop)
		assert.Equal(t, 1*time.Millisecond, mock.DelayReload)
	})

	t.Run("NewMockRunnableWithShutdownSender creates with default delays", func(t *testing.T) {
		mock := mocks.NewMockRunnableWithShutdownSender()
		assert.Equal(t, 1*time.Millisecond, mock.DelayRun)
		assert.Equal(t, 1*time.Millisecond, mock.DelayStop)
		assert.Equal(t, 1*time.Millisecond, mock.DelayReload)
	})
}
