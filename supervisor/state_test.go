package supervisor

import (
	"testing"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
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
