package httpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/robbyt/go-supervisor/internal/finitestate"
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
func (m *MockStateMachine) GetStateChanBuffer(ctx context.Context, bufferSize int) <-chan string {
	args := m.Called(ctx, bufferSize)
	return args.Get(0).(<-chan string)
}

// MockRunner is a special version of Runner that allows direct manipulation
// of config storage for testing purposes
type MockRunner struct {
	storedConfig *Config
	mockFSM      finitestate.Machine
	callback     func() (*Config, error)
	ctx          context.Context
	// Custom error responses for testing
	stopServerErr       error
	setStateErrorCalled bool
}

// Creates a new MockRunner with mocked config storage
func NewMockRunner(configCallback func() (*Config, error), fsm finitestate.Machine) *MockRunner {
	return &MockRunner{
		callback: configCallback,
		mockFSM:  fsm,
		ctx:      context.Background(),
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

// String returns a string representation of the MockRunner
func (r *MockRunner) String() string {
	config := r.getConfig()
	if config == nil {
		return "MockRunner<nil>"
	}
	return fmt.Sprintf("MockRunner{listening: %s}", config.ListenAddr)
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

// stopServer is a mock implementation of Runner.stopServer
func (r *MockRunner) stopServer(ctx context.Context) error {
	return r.stopServerErr
}

// setStateError is a mock implementation of Runner.setStateError
func (r *MockRunner) setStateError() {
	r.setStateErrorCalled = true
	if err := r.mockFSM.SetState(finitestate.StatusError); err != nil {
		// In test code, we just panic on state machine errors to make failures obvious
		panic(fmt.Sprintf("Failed to set error state: %v", err))
	}
}

// Reload is a simplified implementation of the Runner.Reload method for testing
func (r *MockRunner) Reload() {
	// Attempt to transition to reloading state
	if err := r.mockFSM.Transition(finitestate.StatusReloading); err != nil {
		return
	}

	// Try to reload config
	err := r.reloadConfig()
	if err != nil {
		if errors.Is(err, ErrOldConfig) {
			// Config unchanged, go back to running
			if stateErr := r.mockFSM.Transition(finitestate.StatusRunning); stateErr != nil {
				r.setStateError()
			}
			return
		}
		r.setStateError()
		return
	}

	// Try to stop server
	if err := r.stopServer(r.ctx); err != nil {
		r.setStateError()
		return
	}

	// For testing, we'll skip the actual boot() step
	// just transition to running state
	if err := r.mockFSM.Transition(finitestate.StatusRunning); err != nil {
		r.setStateError()
		return
	}
}

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
