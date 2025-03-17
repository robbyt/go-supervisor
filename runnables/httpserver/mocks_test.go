package httpserver

import (
	"context"
	"errors"
	"fmt"

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

// MockRunner is a special version of Runner that allows direct manipulation
// of config storage for testing purposes
type MockRunner struct {
	storedConfig *Config
	mockFSM      stateMachine
	callback     func() (*Config, error)
}

// Creates a new MockRunner with mocked config storage
func NewMockRunner(configCallback func() (*Config, error), fsm stateMachine) *MockRunner {
	return &MockRunner{
		callback: configCallback,
		mockFSM:  fsm,
	}
}

// getConfig implementation for MockRunner
func (r *MockRunner) getConfig() *Config {
	return r.storedConfig
}

// setConfig implementation for MockRunner
func (r *MockRunner) setConfig(config *Config) {
	r.storedConfig = config
}

// configCallback returns the MockRunner's callback
func (r *MockRunner) configCallback() (*Config, error) {
	return r.callback()
}

// reloadConfig implementation similar to Runner's reloadConfig
func (r *MockRunner) reloadConfig() error {
	newConfig, err := r.configCallback()
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	if newConfig == nil {
		return errors.New("config callback returned nil")
	}

	oldConfig := r.getConfig()
	if oldConfig == nil {
		r.setConfig(newConfig)
		return nil
	}

	if newConfig.Equal(oldConfig) {
		// Config unchanged, skip reload and return early
		return ErrOldConfig
	}

	r.setConfig(newConfig)
	return nil
}
