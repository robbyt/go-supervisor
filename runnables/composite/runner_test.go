package composite

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func setupTestLogger() slog.Handler {
	return slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
}

func TestCompositeInterfaceImplementation(t *testing.T) {
	t.Parallel()

	var (
		_ supervisor.Runnable   = (*Runner[supervisor.Runnable])(nil)
		_ supervisor.Reloadable = (*Runner[supervisor.Runnable])(nil)
		_ supervisor.Stateable  = (*Runner[supervisor.Runnable])(nil)
	)
}

func TestNewRunner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		withOptions []Option[*mocks.Runnable]
		expectError bool
	}{
		{
			name: "valid with required options",
			withOptions: []Option[*mocks.Runnable]{
				WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
					entries := []RunnableEntry[*mocks.Runnable]{}
					return NewConfig("test", entries)
				}),
			},
			expectError: false,
		},
		{
			name:        "missing config callback",
			withOptions: []Option[*mocks.Runnable]{},
			expectError: true,
		},
		{
			name: "config callback returns error",
			withOptions: []Option[*mocks.Runnable]{
				WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
					return nil, errors.New("config error")
				}),
			},
			expectError: false,
		},
		{
			name: "config callback returns nil",
			withOptions: []Option[*mocks.Runnable]{
				WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
					return nil, nil
				}),
			},
			expectError: false,
		},
		{
			name: "with custom logger",
			withOptions: []Option[*mocks.Runnable]{
				WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
					entries := []RunnableEntry[*mocks.Runnable]{}
					return NewConfig("test", entries)
				}),
				WithLogHandler[*mocks.Runnable](setupTestLogger()),
			},
			expectError: false,
		},
		{
			name: "with custom context",
			withOptions: []Option[*mocks.Runnable]{
				WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
					entries := []RunnableEntry[*mocks.Runnable]{}
					return NewConfig("test", entries)
				}),
				WithContext[*mocks.Runnable](context.Background()),
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := NewRunner(tt.withOptions...)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, runner)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, runner)
			}
		})
	}
}

func TestCompositeRunner_String(t *testing.T) {
	t.Parallel()

	// Setup mock runnables
	mockRunnable1 := mocks.NewMockRunnable()
	mockRunnable1.On("String").Return("runnable1").Maybe()
	mockRunnable1.On("Stop").Maybe()
	mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()

	mockRunnable2 := mocks.NewMockRunnable()
	mockRunnable2.On("String").Return("runnable2").Maybe()
	mockRunnable2.On("Stop").Maybe()
	mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()

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
		WithConfigCallback[*mocks.Runnable](configCallback),
	)
	require.NoError(t, err)

	// Test String() method
	str := runner.String()
	assert.Contains(t, str, "CompositeRunner")
	assert.Contains(t, str, "test-runner")
	assert.Contains(t, str, "2")
}

func TestCompositeRunner_GetChildStates(t *testing.T) {
	t.Parallel()

	// Create a statable runnable mock
	mockRunnable := mocks.NewMockRunnableWithStatable()
	mockRunnable.On("String").Return("statable-runnable").Maybe()
	mockRunnable.On("GetState").Return("mock-state")
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()

	// Create a regular runnable mock
	regularRunnable := mocks.NewMockRunnable()
	regularRunnable.On("String").Return("regular-runnable").Maybe()
	regularRunnable.On("Stop").Maybe()
	regularRunnable.On("Run", mock.Anything).Return(nil).Maybe()

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
		WithConfigCallback[supervisor.Runnable](configCallback),
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

	// Setup mock runnables
	mockRunnable := mocks.NewMockRunnable()
	mockRunnable.On("String").Return("runnable1").Maybe()
	mockRunnable.On("Stop").Maybe()
	mockRunnable.On("Run", mock.Anything).Return(nil).Maybe()

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
		WithConfigCallback[*mocks.Runnable](configCallback),
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
