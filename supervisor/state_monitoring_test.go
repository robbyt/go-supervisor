package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPIDZero_StartStateMonitor tests that the state monitor is started for stateable runnables.
func TestPIDZero_StartStateMonitor(t *testing.T) {
	t.Parallel()

	// Create a mock stateable runnable
	mockStateable := mocks.NewMockRunnableWithStateable()
	mockStateable.On("String").Return("stateable-runnable").Maybe()
	mockStateable.On("Run", mock.Anything).Return(nil)
	mockStateable.On("Stop").Once()
	mockStateable.On("GetState").Return("initial").Once()  // Initial state
	mockStateable.On("GetState").Return("running").Maybe() // Called during shutdown

	stateChan := make(chan string, 5) // Buffered to prevent blocking
	mockStateable.On("GetStateChan", mock.Anything).Return(stateChan).Once()

	// Will be called during startup verification
	mockStateable.On("IsRunning").Return(true).Once()

	// Create context with timeout to ensure test completion
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create supervisor with the mock runnable
	pid0, err := New(
		WithContext(ctx),
		WithRunnables(mockStateable),
		WithStartupTimeout(100*time.Millisecond),
	)
	require.NoError(t, err)

	// Create a state subscriber to verify state broadcasts
	stateUpdates := make(chan StateMap, 5)
	unsubscribe := pid0.AddStateSubscriber(stateUpdates)
	defer unsubscribe()

	// Start the supervisor in a goroutine
	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	// Allow time for initialization
	time.Sleep(50 * time.Millisecond)

	// Send state updates through the channel
	stateChan <- "initial"  // This should be discarded as it's the initial state
	stateChan <- "running"  // This will be processed
	stateChan <- "stopping" // Additional state change

	// Use require.Eventually to verify the state monitor receives and broadcasts states
	require.Eventually(t, func() bool {
		// Check if we have received at least one state update
		select {
		case stateMap := <-stateUpdates:
			// We don't check for specific values, just that broadcasts are happening
			return stateMap["stateable-runnable"] != ""
		default:
			return false
		}
	}, 500*time.Millisecond, 50*time.Millisecond, "No state updates received")

	// Cancel the context to shut down the supervisor
	cancel()

	// Verify the supervisor shuts down cleanly
	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Supervisor did not shut down in time")
	}

	// Verify expectations
	mockStateable.AssertExpectations(t)
}

// TestPIDZero_SubscribeStateChanges tests the SubscribeStateChanges functionality.
func TestPIDZero_SubscribeStateChanges(t *testing.T) {
	t.Parallel()

	// Create a context with cancel for cleanup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create mock services that implement Stateable
	mockService := mocks.NewMockRunnableWithStateable()
	stateChan := make(chan string, 2)
	mockService.On("GetStateChan", mock.Anything).Return(stateChan).Once()
	mockService.On("String").Return("mock-service").Maybe()
	mockService.On("Run", mock.Anything).Return(nil).Maybe()
	mockService.On("Stop").Maybe()
	mockService.On("GetState").Return("initial").Maybe()
	mockService.On("IsRunning").Return(true).Maybe()

	// Create a supervisor with the mock runnable
	pid0, err := New(WithContext(ctx), WithRunnables(mockService))
	require.NoError(t, err)

	// Store initial states manually in stateMap
	pid0.stateMap.Store(mockService, "initial")

	// Subscribe to state changes
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	stateMapChan := pid0.SubscribeStateChanges(subCtx)

	// Manually call startStateMonitor to avoid the full Run sequence
	pid0.wg.Add(1)
	go pid0.startStateMonitor()

	// Give state monitor a moment to start
	time.Sleep(100 * time.Millisecond)

	// Send an initial state update
	stateChan <- "initial" // Should be discarded

	// Send another state update
	stateChan <- "running" // Should be broadcast
	time.Sleep(100 * time.Millisecond)

	// Manually update state and trigger broadcast to ensure it happens
	pid0.stateMap.Store(mockService, "running")
	pid0.broadcastState()
	time.Sleep(100 * time.Millisecond)

	// Verify we receive state updates
	var stateMap StateMap
	var foundRunning bool
	timeout := time.After(500 * time.Millisecond)

	// Loop until we find the update we want or time out
	for !foundRunning {
		select {
		case stateMap = <-stateMapChan:
			if val, ok := stateMap["mock-service"]; ok && val == "running" {
				foundRunning = true
			}
		case <-timeout:
			t.Fatal("Did not receive running state update in time")
		}
	}

	assert.True(t, foundRunning, "Should have received a state map with running state")

	// Cancel the context to clean up goroutines
	cancel()
	time.Sleep(50 * time.Millisecond)

	// Verify expectations
	mockService.AssertExpectations(t)
}
