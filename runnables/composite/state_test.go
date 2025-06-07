package composite

import (
	"context"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCompositeRunner_GetChildStates(t *testing.T) {
	t.Parallel()

	// Create a stateable runnable mock
	mockRunnable := mocks.NewMockRunnableWithStateable()
	mockRunnable.On("String").Return("stateable-runnable").Once()
	mockRunnable.On("GetState").Return("mock-state").Once()

	// Create a regular runnable mock
	regularRunnable := mocks.NewMockRunnable()
	regularRunnable.On("String").Return("regular-runnable").Once()

	// Create entries for the stateable runnable
	entries := []RunnableEntry[supervisor.Runnable]{
		{Runnable: mockRunnable, Config: nil},
		{Runnable: regularRunnable, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[supervisor.Runnable], error) {
		return NewConfig("test", entries)
	}

	// Create runner with the supervisor.Runnable interface type
	runner, err := NewRunner(configCallback)
	require.NoError(t, err)

	// Get child states
	states := runner.GetChildStates()
	require.Len(t, states, 2)
	assert.Equal(t, "mock-state", states["stateable-runnable"])
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
	runner, err := NewRunner(configCallback)
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

	// Create a runner with callback
	cb := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
	}
	runner, err := NewRunner(cb)
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
	mockFSM.On("GetStateChanWithTimeout", mock.Anything).Return(stateCh)

	// Create a runner
	cb := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
	}

	runner, err := NewRunner(cb)
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
	mockStateable := mocks.NewMockRunnableWithStateable()
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
	runner, err := NewRunner(configCallback)
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
	runner, err := NewRunner(configCallback)
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
	runner, err := NewRunner(configCallback)
	require.NoError(t, err)

	// Clear any cached config
	runner.currentConfig.Store(nil)

	// Get string representation
	str := runner.String()

	// Verify it shows nil config
	assert.Equal(t, "CompositeRunner<nil>", str)
}

// TestGetStateChanWithTimeout tests the GetStateChanWithTimeout method
func TestGetStateChanWithTimeout(t *testing.T) {
	t.Parallel()

	// Create mock runnable
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("String").Return("runnable1").Maybe()

	// Create entries
	entries := []RunnableEntry[*mocks.Runnable]{
		{Runnable: mockRunnable, Config: nil},
	}

	// Create config callback
	configCallback := func() (*Config[*mocks.Runnable], error) {
		return NewConfig("test", entries)
	}

	// Create runner
	runner, err := NewRunner(configCallback)
	require.NoError(t, err)

	t.Run("provides state updates with timeout", func(t *testing.T) {
		// Create context with cancel
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Get state channel with timeout
		stateCh := runner.GetStateChanWithTimeout(ctx)
		require.NotNil(t, stateCh)

		// Should receive initial state
		assert.Eventually(t, func() bool {
			select {
			case state := <-stateCh:
				return state == "New"
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Should receive initial state")

		// Transition to a new state
		err = runner.fsm.Transition("Booting")
		require.NoError(t, err)

		// Should receive updated state
		assert.Eventually(t, func() bool {
			select {
			case state := <-stateCh:
				return state == "Booting"
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Should receive Booting state")

		// Cancel context and verify channel closes
		cancel()

		assert.Eventually(t, func() bool {
			_, open := <-stateCh
			return !open
		}, 1*time.Second, 10*time.Millisecond, "Channel should close when context is canceled")
	})

	t.Run("multiple channels work independently", func(t *testing.T) {
		// Create separate contexts
		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())
		defer cancel1()
		defer cancel2()

		// Get multiple state channels
		stateCh1 := runner.GetStateChanWithTimeout(ctx1)
		stateCh2 := runner.GetStateChanWithTimeout(ctx2)
		require.NotNil(t, stateCh1)
		require.NotNil(t, stateCh2)

		// Drain any initial states from both channels
		select {
		case <-stateCh1:
		default:
		}
		select {
		case <-stateCh2:
		default:
		}

		// Transition state
		err = runner.fsm.Transition("Running")
		require.NoError(t, err)

		// Both channels should receive the update
		assert.Eventually(t, func() bool {
			select {
			case state := <-stateCh1:
				return state == "Running"
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "First channel should receive state update")

		assert.Eventually(t, func() bool {
			select {
			case state := <-stateCh2:
				return state == "Running"
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Second channel should receive state update")

		// Cancel only first context
		cancel1()

		// First channel should close, second should remain open
		assert.Eventually(t, func() bool {
			_, open := <-stateCh1
			return !open
		}, 1*time.Second, 10*time.Millisecond, "First channel should close")

		// Second channel should still be open and receive new state
		err = runner.fsm.Transition("Stopping")
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			select {
			case state := <-stateCh2:
				return state == "Stopping"
			default:
				return false
			}
		}, 1*time.Second, 10*time.Millisecond, "Second channel should still receive updates")
	})

	t.Run("channel closes on context timeout", func(t *testing.T) {
		// Create context with short timeout
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		// Get state channel
		stateCh := runner.GetStateChanWithTimeout(ctx)
		require.NotNil(t, stateCh)

		// Channel should close when context times out
		assert.Eventually(t, func() bool {
			_, open := <-stateCh
			return !open
		}, 100*time.Millisecond, 10*time.Millisecond, "Channel should close on context timeout")
	})
}
