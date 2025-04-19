package supervisor

import (
	"context"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// TestPIDZero_GetState tests the GetState method with Stateable and non-Stateable runnables.
func TestPIDZero_GetState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setupMock     func() Runnable
		expectedState string
	}{
		{
			name: "runnable implements Stateable",
			setupMock: func() Runnable {
				mockService := mocks.NewMockRunnableWithStatable()
				mockService.On("GetState").Return("running").Once()
				mockService.On("String").Return("StatableService").Maybe()
				return mockService
			},
			expectedState: "running",
		},
		{
			name: "runnable does not implement Stateable",
			setupMock: func() Runnable {
				// Create a mock that only implements Runnable but not Stateable
				mockRunnable := mocks.NewMockRunnable()
				// Since we're not actually calling Run or Stop in this test, we only need String
				mockRunnable.On("String").Return("SimpleRunnable").Maybe()
				return mockRunnable
			},
			expectedState: "unknown",
		},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a supervisor with the mock runnable
			runnable := tt.setupMock()
			pidZero, err := New(WithRunnables(runnable))
			assert.NoError(t, err)

			// Get the state of the runnable
			state := pidZero.GetCurrentState(runnable)

			// Verify the state
			assert.Equal(t, tt.expectedState, state)

			// If using a mock, verify expectations
			if m, ok := runnable.(*mocks.Runnable); ok {
				m.AssertExpectations(t)
			}
		})
	}
}

// TestPIDZero_GetStates tests the GetStates method with multiple runnables.
func TestPIDZero_GetStates(t *testing.T) {
	t.Parallel()

	// Create mock services
	mockService1 := mocks.NewMockRunnableWithStatable()
	mockService1.On("GetState").Return("running").Once()
	mockService1.On("String").Return("MockService1").Maybe()

	mockService2 := mocks.NewMockRunnableWithStatable()
	mockService2.On("GetState").Return("stopped").Once()
	mockService2.On("String").Return("MockService2").Maybe()

	// Create a non-Stateable runnable that doesn't implement GetState
	nonStateableRunnable := mocks.NewMockRunnable()
	nonStateableRunnable.On("String").Return("NonStateableRunnable").Maybe()

	// Create a supervisor with the mock runnables
	pidZero, err := New(WithRunnables(mockService1, mockService2, nonStateableRunnable))
	assert.NoError(t, err)

	// Get the states of all runnables
	states := pidZero.GetCurrentStates()

	// Verify the states map
	assert.Len(t, states, 2) // Only Stateable runnables should be in the map
	assert.Equal(t, "running", states[mockService1])
	assert.Equal(t, "stopped", states[mockService2])

	// Verify the non-Stateable runnable is not in the map
	_, exists := states[nonStateableRunnable]
	assert.False(t, exists)

	// Verify expectations
	mockService1.AssertExpectations(t)
	mockService2.AssertExpectations(t)
}

// TestPIDZero_StartStateMonitor tests the startStateMonitor method.
func TestPIDZero_StartStateMonitor(t *testing.T) {
	t.Parallel()

	// Create a context with cancel for cleanup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create mock service that implements Stateable
	mockService := mocks.NewMockRunnableWithStatable()
	stateChan := make(chan string, 3) // Buffered to prevent blocking

	mockService.On("GetState").Return("initial").Maybe()
	mockService.On("GetStateChan", mock.Anything).Return(stateChan).Once()
	mockService.On("String").Return("MockServiceStateMonitor").Maybe()

	// Create a supervisor with the mock runnable
	pidZero, err := New(WithContext(ctx), WithRunnables(mockService))
	assert.NoError(t, err)

	// Manually store initial state to match production behavior
	pidZero.stateMap.Store(mockService, "initial")

	// Start the state monitor
	pidZero.startStateMonitor()

	// Send an initial state that will be discarded
	stateChan <- "initial"
	time.Sleep(50 * time.Millisecond) // Give time for the discard to happen

	// Send state updates through the channel
	stateChan <- "running"
	time.Sleep(50 * time.Millisecond) // Give time for the update to be processed

	// Get the current state from the supervisor's state map
	var state any
	var found bool
	pidZero.stateMap.Range(func(key, value any) bool {
		if key == mockService {
			state = value
			found = true
			return false // Stop iteration
		}
		return true // Continue iteration
	})

	// Verify the state was updated
	assert.True(t, found, "State for mockService should be found in stateMap")
	assert.Equal(t, "running", state, "State should be updated to 'running'")

	// Send another state update
	stateChan <- "stopped"
	time.Sleep(50 * time.Millisecond) // Give time for the update to be processed

	// Get the updated state
	pidZero.stateMap.Range(func(key, value any) bool {
		if key == mockService {
			state = value
			return false // Stop iteration
		}
		return true // Continue iteration
	})

	// Verify the state was updated again
	assert.Equal(t, "stopped", state, "State should be updated to 'stopped'")

	// Cancel the context to clean up goroutines
	cancel()
	time.Sleep(50 * time.Millisecond) // Give time for goroutines to exit

	// Verify expectations
	mockService.AssertExpectations(t)
}

// TestPIDZero_SubscribeStateChanges tests the SubscribeStateChanges method.
func TestPIDZero_SubscribeStateChanges(t *testing.T) {
	t.Parallel()

	// Create a context with cancel for cleanup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create mock services that implement Stateable
	mockService1 := mocks.NewMockRunnableWithStatable()
	stateChan1 := make(chan string, 2)
	mockService1.On("GetStateChan", mock.Anything).Return(stateChan1).Once()
	mockService1.On("String").Return("mock1").Maybe()

	mockService2 := mocks.NewMockRunnableWithStatable()
	stateChan2 := make(chan string, 2)
	mockService2.On("GetStateChan", mock.Anything).Return(stateChan2).Once()
	mockService2.On("String").Return("mock2").Maybe()

	// Create a supervisor with the mock runnables
	pidZero, err := New(WithContext(ctx), WithRunnables(mockService1, mockService2))
	assert.NoError(t, err)

	// Store initial states manually in stateMap
	pidZero.stateMap.Store(mockService1, "initial1")
	pidZero.stateMap.Store(mockService2, "initial2")

	// Start the state monitor
	pidZero.startStateMonitor()

	// Subscribe to state changes
	stateMapChan := pidZero.SubscribeStateChanges(ctx)

	// Read initial state map
	initialStateMap := <-stateMapChan

	// Assert with the string representation of our mocks
	assert.Contains(t, initialStateMap, mockService1.String())
	assert.Contains(t, initialStateMap, mockService2.String())

	// Sleep a bit to ensure the state monitor goroutines are ready
	time.Sleep(100 * time.Millisecond)

	// Update state in the internal map and trigger broadcast
	pidZero.stateMap.Store(mockService1, "running")
	pidZero.broadcastState()

	// Wait for state update to be broadcast
	stateMap := <-stateMapChan
	assert.Equal(t, "running", stateMap[mockService1.String()])
	assert.Equal(t, "initial2", stateMap[mockService2.String()])

	// Update state in the internal map and trigger broadcast
	pidZero.stateMap.Store(mockService2, "stopped")
	pidZero.broadcastState()

	// Wait for state update to be broadcast
	stateMap = <-stateMapChan
	assert.Equal(t, "running", stateMap[mockService1.String()])
	assert.Equal(t, "stopped", stateMap[mockService2.String()])

	// Test unsubscribing
	cancel()
	time.Sleep(50 * time.Millisecond) // Give time for cleanup

	// Verify that no more state updates are received by checking if channel is closed
	_, ok := <-stateMapChan
	assert.False(t, ok, "Channel should be closed after context is cancelled")

	// Verify expectations
	mockService1.AssertExpectations(t)
	mockService2.AssertExpectations(t)
}
