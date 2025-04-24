package composite

import (
	"context"
	"testing"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCompositeRunner_GetChildStates(t *testing.T) {
	t.Parallel()

	// Create a statable runnable mock
	mockRunnable := mocks.NewMockRunnableWithStatable()
	mockRunnable.On("String").Return("statable-runnable").Once()
	mockRunnable.On("GetState").Return("mock-state").Once()

	// Create a regular runnable mock
	regularRunnable := mocks.NewMockRunnable()
	regularRunnable.On("String").Return("regular-runnable").Once()

	// Create entries for the statable runnable
	entries := []RunnableEntry[supervisor.Runnable]{
		{Runnable: mockRunnable, Config: nil},
		{Runnable: regularRunnable, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[supervisor.Runnable], error) {
		return NewConfig("test", entries)
	}

	// Create runner with the supervisor.Runnable interface type
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Get child states
	states := runner.GetChildStates()
	require.Len(t, states, 2)
	assert.Equal(t, "mock-state", states["statable-runnable"])
	assert.Equal(t, "unknown", states["regular-runnable"])

	// Verify expectations
	mockRunnable.AssertExpectations(t)
	regularRunnable.AssertExpectations(t)
}

func TestCompositeRunner_GetStateChan(t *testing.T) {
	t.Parallel()
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("String").Return("runnable1").Once()

	// Create entries
	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", entries)
	}

	// Create runner
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Get state channel
	stateCh := runner.GetStateChan(ctx)
	require.NotNil(t, stateCh)

	// Read initial state
	initialState := <-stateCh
	assert.Equal(t, "New", initialState)

	// Transition to a new state
	err = runner.fsm.Transition("Booting")
	require.NoError(t, err)

	// Read updated state
	select {
	case state := <-stateCh:
		assert.Equal(t, "Booting", state)
	case <-ctx.Done():
		t.Fatal("Context canceled before state update received")
	}

	// Verify that the channel closes when context is canceled
	cancel()

	// Ensure the channel is closed after context is canceled
	_, ok := <-stateCh
	assert.False(t, ok, "Channel should be closed after context is canceled")
}

// TestGetState_PassThrough tests the GetState method passes through to the underlying FSM
func TestGetState_PassThrough(t *testing.T) {
	t.Parallel()

	// Create a mock FSM
	mockFSM := new(MockStateMachine)
	mockFSM.On("GetState").Return(finitestate.StatusRunning)

	// Create a runner
	runner, err := NewRunner(
		WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}),
	)
	require.NoError(t, err)

	// Replace the FSM with our mock
	runner.fsm = mockFSM

	// Call GetState
	state := runner.GetState()

	// Verify it returns the state from the FSM
	assert.Equal(t, finitestate.StatusRunning, state)
	mockFSM.AssertExpectations(t)
}

// TestGetStateChan_PassThrough tests the GetStateChan method passes through to the underlying FSM
func TestGetStateChan_PassThrough(t *testing.T) {
	t.Parallel()

	// Create a test channel to return - must be a receive-only channel
	ch := make(chan string, 1)
	var stateCh <-chan string = ch

	// Create a mock FSM
	mockFSM := new(MockStateMachine)
	mockFSM.On("GetStateChan", mock.Anything).Return(stateCh)

	// Create a runner
	runner, err := NewRunner(
		WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}),
	)
	require.NoError(t, err)

	// Replace the FSM with our mock
	runner.fsm = mockFSM

	// Call GetStateChan
	ctx := context.Background()
	resultCh := runner.GetStateChan(ctx)

	// Verify it returns the channel from the FSM
	assert.Equal(t, stateCh, resultCh)
	mockFSM.AssertExpectations(t)
}

// TestGetChildStates_WithStateables tests the GetChildStates method with stateable runnables
func TestGetChildStates_WithStateables(t *testing.T) {
	t.Parallel()

	// Create a mock stateable runnable
	mockStateable := mocks.NewMockRunnableWithStatable()
	mockStateable.On("String").Return("stateable").Maybe()
	mockStateable.On("GetState").Return("running")
	mockStateable.On("Stop").Maybe()
	mockStateable.On("Run", mock.Anything).Return(nil).Maybe()

	// Create a normal mock runnable (not stateable)
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("String").Return("runnable").Maybe()
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()

	// Create entries with mockStateable
	type mixedRunnable interface {
		supervisor.Runnable
	}

	entries := []RunnableEntry[mixedRunnable]{
		{Runnable: mockStateable, Config: nil},
		{Runnable: mockRunnable, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[mixedRunnable], error) {
		return NewConfig("test", entries)
	}

	// Create runner
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Call GetChildStates
	states := runner.GetChildStates()

	// Verify states
	expected := map[string]string{
		"stateable": "running",
		"runnable":  "unknown",
	}
	assert.Equal(t, expected, states)

	mockStateable.AssertExpectations(t)
}

// TestCompositeRunner_GetStringWithConfig tests the String method with a config
func TestCompositeRunner_GetStringWithConfig(t *testing.T) {
	t.Parallel()

	// Create mock runnables
	mockRunnable1 := mocks.NewMockRunnable()
	mockRunnable1.On("String").Return("runnable1").Maybe()

	mockRunnable2 := mocks.NewMockRunnable()
	mockRunnable2.On("String").Return("runnable2").Maybe()

	// Create entries
	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable1, Config: nil},
		{Runnable: mockRunnable2, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test-runner", entries)
	}

	// Create runner
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Get string representation
	str := runner.String()

	// Verify it includes the config name and entry count
	assert.Contains(t, str, "test-runner")
	assert.Contains(t, str, "entries: 2")
}

// TestCompositeRunner_GetStringWithNilConfig tests the String method with a nil config
func TestCompositeRunner_GetStringWithNilConfig(t *testing.T) {
	t.Parallel()

	// Create config callback that returns nil
	configCallback := func() (*Config[*mocks.Runnable], error) {
		return nil, nil
	}

	// Create runner
	runner, err := NewRunner(
		WithConfigCallback(configCallback),
	)
	require.NoError(t, err)

	// Clear any cached config
	runner.currentConfig.Store(nil)

	// Get string representation
	str := runner.String()

	// Verify it shows nil config
	assert.Equal(t, "CompositeRunner<nil>", str)
}
