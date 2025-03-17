package httpserver

import (
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-fsm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSetStateError_Mocked(t *testing.T) {
	t.Parallel()

	// Test successful TransitionBool path
	t.Run("Success with TransitionBool", func(t *testing.T) {
		// Create mock state machine
		mockFSM := NewMockStateMachine()

		// Setup the TransitionBool to return success
		mockFSM.On("TransitionBool", fsm.StatusError).Return(true)

		// Create runner with mocked FSM
		r := &Runner{
			fsm:    mockFSM,
			logger: slog.Default().WithGroup("httpserver.Runner"),
		}

		// Call the function under test
		r.setStateError()

		// Verify our expectations
		mockFSM.AssertExpectations(t)

		// TransitionBool should have been called once, but SetState should not be called
		mockFSM.AssertCalled(t, "TransitionBool", fsm.StatusError)
		mockFSM.AssertNotCalled(t, "SetState", mock.Anything)
	})

	// Test fallback to SetState when TransitionBool fails
	t.Run("Fallback to SetState when TransitionBool fails", func(t *testing.T) {
		// Create mock state machine
		mockFSM := NewMockStateMachine()

		// Setup the TransitionBool to return failure
		mockFSM.On("TransitionBool", fsm.StatusError).Return(false)

		// Setup the SetState to succeed
		mockFSM.On("SetState", fsm.StatusError).Return(nil)

		// Create runner with mocked FSM
		r := &Runner{
			fsm:    mockFSM,
			logger: slog.Default().WithGroup("httpserver.Runner"),
		}

		// Call the function under test
		r.setStateError()

		// Verify our expectations
		mockFSM.AssertExpectations(t)

		// Both TransitionBool and SetState should have been called
		mockFSM.AssertCalled(t, "TransitionBool", fsm.StatusError)
		mockFSM.AssertCalled(t, "SetState", fsm.StatusError)

		// The Unknown state should not have been used
		mockFSM.AssertNotCalled(t, "SetState", fsm.StatusUnknown)
	})

	// Test fallback to StatusUnknown when both TransitionBool and the first SetState fail
	t.Run("Fallback to StatusUnknown when both previous methods fail", func(t *testing.T) {
		// Create mock state machine
		mockFSM := NewMockStateMachine()

		// Create a test error
		testErr := errors.New("cannot set error state")

		// Setup the TransitionBool to return failure
		mockFSM.On("TransitionBool", fsm.StatusError).Return(false)

		// Setup the first SetState to fail
		mockFSM.On("SetState", fsm.StatusError).Return(testErr)

		// Setup the fallback SetState to succeed
		mockFSM.On("SetState", fsm.StatusUnknown).Return(nil)

		// Create runner with mocked FSM
		r := &Runner{
			fsm:    mockFSM,
			logger: slog.Default().WithGroup("httpserver.Runner"),
		}

		// Call the function under test
		r.setStateError()

		// Verify our expectations
		mockFSM.AssertExpectations(t)

		// All three method calls should have been made
		mockFSM.AssertCalled(t, "TransitionBool", fsm.StatusError)
		mockFSM.AssertCalled(t, "SetState", fsm.StatusError)
		mockFSM.AssertCalled(t, "SetState", fsm.StatusUnknown)
	})

	// Test complete failure case where all attempts fail
	t.Run("Complete failure when all state transitions fail", func(t *testing.T) {
		// Create mock state machine
		mockFSM := NewMockStateMachine()

		// Create test errors
		errorStateErr := errors.New("cannot set error state")
		unknownStateErr := errors.New("cannot set unknown state either")

		// Setup the TransitionBool to return failure
		mockFSM.On("TransitionBool", fsm.StatusError).Return(false)

		// Setup the first SetState to fail
		mockFSM.On("SetState", fsm.StatusError).Return(errorStateErr)

		// Setup the fallback SetState to also fail
		mockFSM.On("SetState", fsm.StatusUnknown).Return(unknownStateErr)

		// Create runner with mocked FSM
		r := &Runner{
			fsm:    mockFSM,
			logger: slog.Default().WithGroup("httpserver.Runner"),
		}

		// Call the function under test
		r.setStateError()

		// Verify our expectations
		mockFSM.AssertExpectations(t)

		// All three method calls should have been made
		mockFSM.AssertCalled(t, "TransitionBool", fsm.StatusError)
		mockFSM.AssertCalled(t, "SetState", fsm.StatusError)
		mockFSM.AssertCalled(t, "SetState", fsm.StatusUnknown)
	})
}

// TestSetStateError_FullIntegration does an end-to-end verification of setStateError
// using a real FSM to complement the mocked tests
func TestSetStateError_FullIntegration(t *testing.T) {
	t.Parallel()

	server, _, _ := createTestServer(t,
		func(w http.ResponseWriter, r *http.Request) {}, "/test", 1*time.Second)

	// Set up initial conditions with FSM in Stopped state
	err := server.fsm.SetState(fsm.StatusStopped)
	require.NoError(t, err)

	// Call the function under test which should fall back to SetState
	server.setStateError()

	// Verify we ended up in Error state
	assert.Equal(t, fsm.StatusError, server.fsm.GetState())
}
