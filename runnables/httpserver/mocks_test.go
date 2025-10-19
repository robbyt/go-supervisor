package httpserver

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// MockStateMachine is a mock implementation of the stateMachine interface
// for testing purposes. It uses testify/mock to record and verify calls.
type MockStateMachine struct {
	mock.Mock
}

// NewMockStateMachine creates a new instance of MockStateMachine
func NewMockStateMachine() *MockStateMachine {
	return &MockStateMachine{}
}

// Transition mocks the Transition method of the stateMachine interface.
// It attempts to transition the state machine to the specified state.
func (m *MockStateMachine) Transition(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

// TransitionBool mocks the TransitionBool method of the stateMachine interface.
// It attempts to transition the state machine to the specified state and returns
// a boolean indicating success or failure.
func (m *MockStateMachine) TransitionBool(state string) bool {
	args := m.Called(state)
	return args.Bool(0)
}

// TransitionIfCurrentState mocks the TransitionIfCurrentState method of the stateMachine interface.
// It attempts to transition the state machine to the specified state only if the current state
// matches the expected current state.
func (m *MockStateMachine) TransitionIfCurrentState(currentState, newState string) error {
	args := m.Called(currentState, newState)
	return args.Error(0)
}

// SetState mocks the SetState method of the stateMachine interface.
// It sets the state of the state machine to the specified state.
func (m *MockStateMachine) SetState(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

// GetState mocks the GetState method of the stateMachine interface.
// It returns the current state of the state machine.
func (m *MockStateMachine) GetState() string {
	args := m.Called()
	return args.String(0)
}

// GetStateChan mocks the GetStateChan method of the stateMachine interface.
// It returns a channel that emits the state machine's state whenever it changes.
func (m *MockStateMachine) GetStateChan(ctx context.Context) <-chan string {
	args := m.Called(ctx)
	return args.Get(0).(<-chan string)
}

// GetStateChanBuffer mocks the GetStateChanBuffer method of the stateMachine interface.
// It returns a channel with a configurable buffer size that emits the state machine's state whenever it changes.

// MockHttpServer is a mock implementation of the HttpServer interface
type MockHttpServer struct {
	mock.Mock
}

// ListenAndServe mocks the ListenAndServe method of the HttpServer interface
func (m *MockHttpServer) ListenAndServe() error {
	args := m.Called()
	return args.Error(0)
}

// Shutdown mocks the Shutdown method of the HttpServer interface
func (m *MockHttpServer) Shutdown(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
