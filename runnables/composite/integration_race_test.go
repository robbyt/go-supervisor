package composite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestIntegration_CompositeNoRaceCondition verifies that when composite runner
// reports as running, all child runnables are actually running.
// This tests the real FSM implementation, not mocks.
func TestIntegration_CompositeNoRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	const iterations = 5 // Test multiple times to catch race conditions

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			testCompositeRaceCondition(t)
		})
	}
}

func testCompositeRaceCondition(t *testing.T) {
	t.Helper()
	// Create mock runnables using the mocks package
	mock1 := mocks.NewMockRunnableWithStateable()
	mock2 := mocks.NewMockRunnableWithStateable()
	mock3 := mocks.NewMockRunnableWithStateable()
	mock1.On("String").Return("service1")
	mock1.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done() // Block until cancelled like a real service
	})
	mock1.On("Stop").Return()
	mock1.On("IsRunning").Return(true)
	mock1.On("GetState").Return("Running")

	mock2.On("String").Return("service2")
	mock2.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done() // Block until cancelled like a real service
	})
	mock2.On("Stop").Return()
	mock2.On("IsRunning").Return(true)
	mock2.On("GetState").Return("Running")

	mock3.On("String").Return("service3")
	mock3.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done() // Block until cancelled like a real service
	})
	mock3.On("Stop").Return()
	mock3.On("IsRunning").Return(true)
	mock3.On("GetState").Return("Running")

	// Create config callback
	callback := func() (*Config[*mocks.MockRunnableWithStateable], error) {
		return NewConfig("test-composite", []RunnableEntry[*mocks.MockRunnableWithStateable]{
			{Runnable: mock1},
			{Runnable: mock2},
			{Runnable: mock3},
		})
	}

	// Create composite runner with real FSM
	runner, err := NewRunner(callback)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	// Start runner
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	// Wait for IsRunning() to return true
	require.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 5*time.Second, 50*time.Millisecond, "Composite should report as running")

	// Wait for all Run() methods to be called using Eventually
	require.Eventually(t, func() bool {
		// Check if all three mocks have been called with Run
		return mock1.AssertCalled(t, "Run", mock.Anything) &&
			mock2.AssertCalled(t, "Run", mock.Anything) &&
			mock3.AssertCalled(t, "Run", mock.Anything)
	}, 5*time.Second, 50*time.Millisecond, "All mocks should have Run() called")

	// CRITICAL TEST: When composite reports running, all children should be running
	assert.True(t, mock1.IsRunning(),
		"RACE CONDITION: Composite reports running but child 1 not running")
	assert.True(t, mock2.IsRunning(),
		"RACE CONDITION: Composite reports running but child 2 not running")
	assert.True(t, mock3.IsRunning(),
		"RACE CONDITION: Composite reports running but child 3 not running")

	// Test child states through composite
	childStates := runner.GetChildStates()
	assert.Len(t, childStates, 3, "Should have 3 child states")

	for name, state := range childStates {
		assert.Equal(t, "Running", state, "Child %s should be running", name)
	}

	// Stop the runner
	cancel()

	// Wait for shutdown
	timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer timeoutCancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-timeoutCtx.Done():
		t.Fatal("Composite did not shutdown within timeout")
	}

	// Verify Stop() was called on all mocks
	mock1.AssertCalled(t, "Stop")
	mock2.AssertCalled(t, "Stop")
	mock3.AssertCalled(t, "Stop")
}

// TestIntegration_CompositeFullLifecycle tests complete composite lifecycle
func TestIntegration_CompositeFullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create mock runnables for testing
	mock1 := mocks.NewMockRunnableWithStateable()
	mock2 := mocks.NewMockRunnableWithStateable()

	// Set up mock expectations for normal operation
	mock1.On("String").Return("mock-service-1")
	mock1.On("Run", mock.Anything).Return(nil)
	mock1.On("Stop").Return()
	mock1.On("GetState").Return("Running")

	mock2.On("String").Return("mock-service-2")
	mock2.On("Run", mock.Anything).Return(nil)
	mock2.On("Stop").Return()
	mock2.On("GetState").Return("Running")

	// Create config
	callback := func() (*Config[*mocks.MockRunnableWithStateable], error) {
		return NewConfig("integration-test", []RunnableEntry[*mocks.MockRunnableWithStateable]{
			{Runnable: mock1},
			{Runnable: mock2},
		})
	}

	// Create runner
	runner, err := NewRunner(callback)
	require.NoError(t, err)

	// Initial state
	assert.Equal(t, "New", runner.GetState())
	assert.False(t, runner.IsRunning())

	ctx, cancel := context.WithCancel(t.Context())

	// Start runner
	runErr := make(chan error, 1)
	go func() {
		runErr <- runner.Run(ctx)
	}()

	// Should transition through states
	assert.Eventually(t, func() bool {
		state := runner.GetState()
		return state == "Booting" || state == "Running"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Booting")

	// Wait for Running state
	assert.Eventually(t, func() bool {
		return runner.IsRunning() && runner.GetState() == "Running"
	}, 5*time.Second, 50*time.Millisecond, "Should transition to Running")

	// Verify all mocks received Run() calls
	time.Sleep(100 * time.Millisecond) // Give time for calls to register
	mock1.AssertCalled(t, "Run", mock.Anything)
	mock2.AssertCalled(t, "Run", mock.Anything)

	// Test child states
	childStates := runner.GetChildStates()
	assert.Len(t, childStates, 2)
	assert.Equal(t, "Running", childStates["mock-service-1"])
	assert.Equal(t, "Running", childStates["mock-service-2"])

	// Stop runner
	cancel()

	// Should transition to stopping/stopped
	assert.Eventually(t, func() bool {
		state := runner.GetState()
		return state == "Stopping" || state == "Stopped"
	}, 2*time.Second, 50*time.Millisecond, "Should transition to Stopping")

	// Wait for shutdown
	timeoutCtx, timeoutCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer timeoutCancel()
	select {
	case err := <-runErr:
		require.NoError(t, err)
	case <-timeoutCtx.Done():
		t.Fatal("Runner did not shutdown within timeout")
	}

	// Final state
	assert.Eventually(t, func() bool {
		return runner.GetState() == "Stopped"
	}, 1*time.Second, 10*time.Millisecond, "Should be Stopped")

	assert.False(t, runner.IsRunning())

	// Verify Stop() was called on all mocks
	mock1.AssertCalled(t, "Stop")
	mock2.AssertCalled(t, "Stop")
}
