package httpcluster

import (
	"context"
	"testing"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetState(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner()
	require.NoError(t, err)

	// Initial state should be New
	assert.Equal(t, finitestate.StatusNew, runner.GetState())
}

func TestGetStateChan(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(WithStateChanBufferSize(5))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stateChan := runner.GetStateChan(ctx)
	require.NotNil(t, stateChan)

	// Should receive the initial state
	select {
	case state := <-stateChan:
		assert.Equal(t, finitestate.StatusNew, state)
	default:
		t.Fatal("Expected to receive initial state")
	}
}

func TestGetStateChanWithTimeout(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner(WithStateChanBufferSize(5))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	stateChan := runner.GetStateChanWithTimeout(ctx)
	require.NotNil(t, stateChan)

	// Should receive the initial state
	select {
	case state := <-stateChan:
		assert.Equal(t, finitestate.StatusNew, state)
	default:
		t.Fatal("Expected to receive initial state")
	}
}

func TestIsRunning(t *testing.T) {
	t.Parallel()

	runner, err := NewRunner()
	require.NoError(t, err)

	// Initially not running
	assert.False(t, runner.IsRunning())

	// Transition to running state
	err = runner.fsm.Transition(finitestate.StatusBooting)
	require.NoError(t, err)
	err = runner.fsm.Transition(finitestate.StatusRunning)
	require.NoError(t, err)

	// Now should be running
	assert.True(t, runner.IsRunning())
}

// MockFSMForStateError is a mock FSM that can simulate error conditions
type MockFSMForStateError struct {
	mock.Mock
}

func (m *MockFSMForStateError) Transition(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

func (m *MockFSMForStateError) TransitionBool(state string) bool {
	args := m.Called(state)
	return args.Bool(0)
}

func (m *MockFSMForStateError) TransitionIfCurrentState(currentState, newState string) error {
	args := m.Called(currentState, newState)
	return args.Error(0)
}

func (m *MockFSMForStateError) SetState(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

func (m *MockFSMForStateError) GetState() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockFSMForStateError) GetStateChan(ctx context.Context) <-chan string {
	args := m.Called(ctx)
	return args.Get(0).(<-chan string)
}

func TestSetStateError(t *testing.T) {
	t.Parallel()

	t.Run("successful transition to error state", func(t *testing.T) {
		mockFSM := &MockFSMForStateError{}
		runner, err := NewRunner()
		require.NoError(t, err)

		// Replace FSM with mock
		runner.fsm = mockFSM

		// Mock successful transition to error state
		mockFSM.On("TransitionBool", finitestate.StatusError).Return(true).Once()

		// Call setStateError - should succeed on first attempt
		runner.setStateError()

		mockFSM.AssertExpectations(t)
	})

	t.Run("fallback to SetState when TransitionBool fails", func(t *testing.T) {
		mockFSM := &MockFSMForStateError{}
		runner, err := NewRunner()
		require.NoError(t, err)

		// Replace FSM with mock
		runner.fsm = mockFSM

		// Mock failed transition but successful SetState
		mockFSM.On("TransitionBool", finitestate.StatusError).Return(false).Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(nil).Once()

		// Call setStateError - should use fallback
		runner.setStateError()

		mockFSM.AssertExpectations(t)
	})

	t.Run("fallback to unknown state when both error state attempts fail", func(t *testing.T) {
		mockFSM := &MockFSMForStateError{}
		runner, err := NewRunner()
		require.NoError(t, err)

		// Replace FSM with mock
		runner.fsm = mockFSM

		// Mock all attempts failing except unknown
		mockFSM.On("TransitionBool", finitestate.StatusError).Return(false).Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(assert.AnError).Once()
		mockFSM.On("SetState", finitestate.StatusUnknown).Return(nil).Once()

		// Call setStateError - should fallback to unknown state
		runner.setStateError()

		mockFSM.AssertExpectations(t)
	})

	t.Run("all state setting attempts fail", func(t *testing.T) {
		mockFSM := &MockFSMForStateError{}
		runner, err := NewRunner()
		require.NoError(t, err)

		// Replace FSM with mock
		runner.fsm = mockFSM

		// Mock all attempts failing
		mockFSM.On("TransitionBool", finitestate.StatusError).Return(false).Once()
		mockFSM.On("SetState", finitestate.StatusError).Return(assert.AnError).Once()
		mockFSM.On("SetState", finitestate.StatusUnknown).Return(assert.AnError).Once()

		// Call setStateError - should handle all failures gracefully
		runner.setStateError()

		mockFSM.AssertExpectations(t)
	})
}
