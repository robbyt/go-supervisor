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
				mockService := mocks.NewMockRunnable()
				mockService.On("GetState").Return("running").Once()
				return mockService
			},
			expectedState: "running",
		},
		{
			name: "runnable does not implement Stateable",
			setupMock: func() Runnable {
				// Create a mock that only implements Runnable but not Stateable
				m := new(mock.Mock)
				m.On("Run", mock.Anything).Return(nil)
				m.On("Stop")

				// Create a simple runnable implementation that wraps the mock
				return &simpleRunnable{
					m: m,
				}
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

// Simple implementation of a Runnable that doesn't implement Stateable
type simpleRunnable struct {
	m *mock.Mock
}

func (s *simpleRunnable) Run(ctx context.Context) error {
	args := s.m.Called(ctx)
	return args.Error(0)
}

func (s *simpleRunnable) Stop() {
	s.m.Called()
}

// TestPIDZero_GetStates tests the GetStates method with multiple runnables.
func TestPIDZero_GetStates(t *testing.T) {
	t.Parallel()

	// Create mock services
	mockService1 := mocks.NewMockRunnable()
	mockService1.On("GetState").Return("running").Once()

	mockService2 := mocks.NewMockRunnable()
	mockService2.On("GetState").Return("stopped").Once()

	// Create a non-Stateable runnable
	m := new(mock.Mock)
	m.On("Run", mock.Anything).Return(nil)
	m.On("Stop")

	nonStateableRunnable := &simpleRunnable{
		m: m,
	}

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
	mockService := mocks.NewMockRunnable()
	stateChan := make(chan string, 3) // Buffered to prevent blocking

	mockService.On("GetState").Return("initial").Maybe()
	mockService.On("GetStateChan", mock.Anything).Return(stateChan).Once()

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
	mockService1 := mocks.NewMockRunnable()
	stateChan1 := make(chan string, 2)
	mockService1.On("GetStateChan", mock.Anything).Return(stateChan1).Once()
	mockService1.On("String").Return("mock1").Maybe()

	mockService2 := mocks.NewMockRunnable()
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
