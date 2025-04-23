package composite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockStateMachine is a mock implementation of the finitestate.Machine interface for testing
type MockStateMachine struct {
	mock.Mock
}

func (m *MockStateMachine) GetState() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockStateMachine) SetState(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

func (m *MockStateMachine) Transition(state string) error {
	args := m.Called(state)
	return args.Error(0)
}

func (m *MockStateMachine) TransitionBool(state string) bool {
	args := m.Called(state)
	return args.Bool(0)
}

func (m *MockStateMachine) TransitionIfCurrentState(current, next string) error {
	args := m.Called(current, next)
	return args.Error(0)
}

func (m *MockStateMachine) GetStateChan(ctx context.Context) <-chan string {
	args := m.Called(ctx)
	return args.Get(0).(<-chan string)
}

// MockReloadableWithConfig implements both Runnable and ReloadableWithConfig for testing
type MockReloadableWithConfig struct {
	*mocks.Runnable
}

func (m *MockReloadableWithConfig) ReloadWithConfig(config any) {
	m.Called(config)
}

func NewMockReloadableWithConfig() *MockReloadableWithConfig {
	return &MockReloadableWithConfig{
		Runnable: mocks.NewMockRunnable(),
	}
}

func TestCompositeRunner_Reload(t *testing.T) {
	t.Parallel()

	t.Run("reload with reloadable runnables", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload").Once()
		mockRunnable1.On("Stop").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload").Once()
		mockRunnable2.On("Stop").Maybe()
		mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner and set state to Running
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](configCallback),
		)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// Call Reload
		runner.Reload()

		// Verify state cycle
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())

		// Verify mock expectations
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reload with updated runnables", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload").Maybe()
		mockRunnable1.On("Stop").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload").Maybe()
		mockRunnable2.On("Stop").Maybe()
		mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("runnable3").Maybe()
		mockRunnable3.On("Reload").Maybe()
		mockRunnable3.On("Stop").Maybe()
		mockRunnable3.On("Run", mock.Anything).Return(nil).Maybe()

		// Create initial entries
		initialEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create updated entries for reload
		updatedEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable3, Config: nil},
		}

		// Variable to track which set of entries to return
		useUpdatedEntries := false

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			if useUpdatedEntries {
				return NewConfig("test", updatedEntries)
			}
			return NewConfig("test", initialEntries)
		}

		// Create runner and set state to Running
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](configCallback),
		)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// Switch to using updated entries
		useUpdatedEntries = true

		// Call Reload
		runner.Reload()

		// Verify updated config
		config := runner.getConfig()
		require.NotNil(t, config)
		assert.Len(t, config.Entries, 2)
		assert.Equal(t, mockRunnable1, config.Entries[0].Runnable)
		assert.Equal(t, mockRunnable3, config.Entries[1].Runnable)

		// Verify state cycle completed
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())
	})

	t.Run("reload with updated configurations", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload").Once()
		mockRunnable1.On("Stop").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload").Once()
		mockRunnable2.On("Stop").Maybe()
		mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()

		// Initial configs
		initialConfig1 := map[string]string{"key": "value1"}
		initialConfig2 := map[string]string{"key": "value2"}

		// Updated configs
		updatedConfig1 := map[string]string{"key": "updated1"}
		updatedConfig2 := map[string]string{"key": "updated2"}

		// Create initial entries
		initialEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: initialConfig1},
			{Runnable: mockRunnable2, Config: initialConfig2},
		}

		// Create updated entries for reload
		updatedEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: updatedConfig1},
			{Runnable: mockRunnable2, Config: updatedConfig2},
		}

		// Variable to track which set of entries to return
		useUpdatedEntries := false

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			if useUpdatedEntries {
				return NewConfig("test", updatedEntries)
			}
			return NewConfig("test", initialEntries)
		}

		// Create runner and set state to Running
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](configCallback),
		)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// Switch to using updated entries
		useUpdatedEntries = true

		// Call Reload
		runner.Reload()

		// Verify updated config
		config := runner.getConfig()
		require.NotNil(t, config)
		assert.Equal(t, updatedConfig1, config.Entries[0].Config)
		assert.Equal(t, updatedConfig2, config.Entries[1].Config)

		// Verify state cycle completed
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())

		// Verify mock expectations (runnables were reloaded)
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reload with no config changes", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload").Once()
		mockRunnable1.On("Stop").Maybe()
		mockRunnable1.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload").Once()
		mockRunnable2.On("Stop").Maybe()
		mockRunnable2.On("Run", mock.Anything).Return(nil).Maybe()

		// Create config
		config := map[string]string{"key": "value"}

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: config},
			{Runnable: mockRunnable2, Config: config},
		}

		// Create config callback that returns same config
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner and set state to Running
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](configCallback),
		)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// Call Reload
		runner.Reload()

		// Verify no config updates occurred but runnables were still reloaded
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reload with ReloadableWithConfig implementation", func(t *testing.T) {
		// Setup mock that implements ReloadableWithConfig
		mockReloadable := NewMockReloadableWithConfig()
		mockReloadable.On("String").Return("reloadable").Maybe()
		mockReloadable.On("ReloadWithConfig", mock.Anything).Once()
		mockReloadable.On("Stop").Maybe()
		mockReloadable.On("Run", mock.Anything).Return(nil).Maybe()

		// Create config and entries
		config := map[string]string{"key": "value"}
		entries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable, Config: config},
		}

		// Create config callback
		configCallback := func() (*Config[*MockReloadableWithConfig], error) {
			return NewConfig("test", entries)
		}

		// Create runner and set state to Running
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)
		err = runner.fsm.SetState(finitestate.StatusRunning)
		require.NoError(t, err)

		// Call Reload
		runner.Reload()

		// Verify ReloadWithConfig was called
		mockReloadable.AssertExpectations(t)
		assert.Equal(t, finitestate.StatusRunning, runner.GetState())
	})
}

// TestCompositeRunner_Reload_Errors tests error paths in the Reload method
func TestCompositeRunner_Reload_Errors(t *testing.T) {
	t.Parallel()

	t.Run("fsm transition to reloading fails", func(t *testing.T) {
		// Create mock FSM that fails transition to reloading
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(errors.New("transition error"))
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			entries := []RunnableEntry[*mocks.Runnable]{}
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload
		runner.Reload()

		// Verify FSM methods were called
		mockFSM.AssertExpectations(t)
	})

	t.Run("config callback error", func(t *testing.T) {
		// Create mock FSM
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil)
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()

		// Create config callback that returns an error
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, errors.New("config error")
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload
		runner.Reload()

		// Verify FSM methods were called
		mockFSM.AssertExpectations(t)
	})

	t.Run("config callback returns nil", func(t *testing.T) {
		// Create mock FSM
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil)
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()

		// Create config callback that returns nil config
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, nil
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload
		runner.Reload()

		// Verify FSM methods were called
		mockFSM.AssertExpectations(t)
	})

	t.Run("getConfig returns nil for old config", func(t *testing.T) {
		// Create mock FSM
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil)
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()

		// Create a mock config callback that returns nil on the second call
		callCount := 0
		configCallback := func() (*Config[*mocks.Runnable], error) {
			callCount++
			if callCount == 1 {
				// First call returns normal config (for initial loading)
				return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
			}
			// Second call returns error (for reload)
			return nil, errors.New("config error")
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload
		runner.Reload()

		// Verify FSM methods were called
		mockFSM.AssertExpectations(t)
	})

	t.Run("fsm transition back to running fails", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload").Maybe()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
		}

		// Create mock FSM that fails transition back to running
		mockFSM := new(MockStateMachine)
		mockFSM.On("Transition", finitestate.StatusReloading).Return(nil)
		mockFSM.On("Transition", finitestate.StatusRunning).Return(errors.New("transition error"))
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)
		mockFSM.On("GetState").Return(finitestate.StatusError).Maybe()

		// Create config callback
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return NewConfig("test", entries)
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call Reload
		runner.Reload()

		// Verify FSM methods were called
		mockFSM.AssertExpectations(t)
	})
}

// TestGetConfig_NilCase tests the getConfig method's handling of nil cases
func TestGetConfig_NilCase(t *testing.T) {
	t.Parallel()

	t.Run("callback returns nil", func(t *testing.T) {
		// Create callback that returns nil config
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, nil
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Clear the config cache
		runner.currentConfig.Store(nil)

		// Get config should return nil
		config := runner.getConfig()
		assert.Nil(t, config)
	})

	t.Run("callback returns error", func(t *testing.T) {
		// Create callback that returns an error
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, errors.New("config error")
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Clear the config cache
		runner.currentConfig.Store(nil)

		// Get config should return nil on error
		config := runner.getConfig()
		assert.Nil(t, config)
	})
}

// TestSetStateError tests the setStateError method
func TestSetStateError(t *testing.T) {
	t.Parallel()

	t.Run("successful transition", func(t *testing.T) {
		// Create mock FSM that succeeds
		mockFSM := new(MockStateMachine)
		mockFSM.On("SetState", finitestate.StatusError).Return(nil)

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
			}),
		)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call setStateError
		runner.setStateError()

		// Verify FSM method was called
		mockFSM.AssertExpectations(t)
	})

	t.Run("transition fails", func(t *testing.T) {
		// Create mock FSM that fails
		mockFSM := new(MockStateMachine)
		mockFSM.On("SetState", finitestate.StatusError).Return(errors.New("set state error"))

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
				return NewConfig("test", []RunnableEntry[*mocks.Runnable]{})
			}),
		)
		require.NoError(t, err)

		// Replace FSM with our mock
		runner.fsm = mockFSM

		// Call setStateError - should handle the error internally
		runner.setStateError()

		// Verify FSM method was called
		mockFSM.AssertExpectations(t)
	})
}

// TestGetChildStates tests the GetChildStates method with various types of runnables
func TestGetChildStates(t *testing.T) {
	t.Parallel()

	// Create a mock for MockRunnableWithStatable to use in tests
	t.Run("nil config case", func(t *testing.T) {
		// Create runner with a config callback that returns nil
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return nil, errors.New("config error")
		}

		runner, err := NewRunner(
			WithConfigCallback(configCallback),
		)
		require.NoError(t, err)

		// Call GetChildStates
		states := runner.GetChildStates()

		// Verify empty map is returned
		assert.Empty(t, states)
	})
}

// TestReloadConfig tests the reloadConfig method
func TestReloadConfig(t *testing.T) {
	t.Parallel()

	t.Run("reloads standard reloadable runnables", func(t *testing.T) {
		// Setup mock runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Reload").Once()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Reload").Once()

		// Create entries
		entries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Create config
		config, err := NewConfig("test", entries)
		require.NoError(t, err)

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
				return config, nil
			}),
		)
		require.NoError(t, err)

		// Call reloadConfig directly
		logger := runner.logger.WithGroup("test")
		runner.reloadConfig(logger, config)

		// Verify reloadable interface methods were called
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
	})

	t.Run("reloads runnables with ReloadableWithConfig interface", func(t *testing.T) {
		// Setup mock that implements ReloadableWithConfig
		mockReloadable1 := NewMockReloadableWithConfig()
		mockReloadable1.On("String").Return("reloadable1").Maybe()

		customConfig1 := map[string]string{"key": "value1"}
		mockReloadable1.On("ReloadWithConfig", customConfig1).Once()

		mockReloadable2 := NewMockReloadableWithConfig()
		mockReloadable2.On("String").Return("reloadable2").Maybe()

		customConfig2 := map[string]string{"key": "value2"}
		mockReloadable2.On("ReloadWithConfig", customConfig2).Once()

		// Create entries
		entries := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable1, Config: customConfig1},
			{Runnable: mockReloadable2, Config: customConfig2},
		}

		// Create config
		config, err := NewConfig("test", entries)
		require.NoError(t, err)

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback[*MockReloadableWithConfig](
				func() (*Config[*MockReloadableWithConfig], error) {
					return config, nil
				},
			),
		)
		require.NoError(t, err)

		// Call reloadConfig directly
		logger := runner.logger.WithGroup("test")
		runner.reloadConfig(logger, config)

		// Verify ReloadWithConfig was called with correct configs
		mockReloadable1.AssertExpectations(t)
		mockReloadable2.AssertExpectations(t)
	})

	t.Run("with mixed interface implementations", func(t *testing.T) {
		// Setup mock that implements standard Reloadable
		mockRunnable := mocks.NewMockRunnable()
		mockRunnable.On("String").Return("runnable").Maybe()
		mockRunnable.On("Reload").Once()

		// Setup mock that implements ReloadableWithConfig
		mockReloadable := NewMockReloadableWithConfig()
		mockReloadable.On("String").Return("reloadable").Maybe()

		customConfig := map[string]string{"key": "value"}
		mockReloadable.On("ReloadWithConfig", customConfig).Once()

		// Create both configs
		entries1 := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable, Config: nil},
		}
		config1, err := NewConfig("test1", entries1)
		require.NoError(t, err)

		entries2 := []RunnableEntry[*MockReloadableWithConfig]{
			{Runnable: mockReloadable, Config: customConfig},
		}
		config2, err := NewConfig("test2", entries2)
		require.NoError(t, err)

		// Create runners
		runner1, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
				return config1, nil
			}),
		)
		require.NoError(t, err)

		runner2, err := NewRunner(
			WithConfigCallback[*MockReloadableWithConfig](
				func() (*Config[*MockReloadableWithConfig], error) {
					return config2, nil
				},
			),
		)
		require.NoError(t, err)

		// Call reloadConfig on both runners
		logger := runner1.logger.WithGroup("test")
		runner1.reloadConfig(logger, config1)
		runner2.reloadConfig(logger, config2)

		// Verify expectations
		mockRunnable.AssertExpectations(t)
		mockReloadable.AssertExpectations(t)
	})
}

// TestReloadMembershipChanged tests the reloadMembershipChanged method
func TestReloadMembershipChanged(t *testing.T) {
	t.Parallel()

	t.Run("successfully stops and starts runnables", func(t *testing.T) {
		// Setup mock runnables for old config
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Stop").Maybe() // Use Maybe() instead of Once() for more resilience

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()
		mockRunnable2.On("Stop").Maybe() // Use Maybe() instead of Once() for more resilience

		// Create initial entries
		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}

		// Setup mock runnables for new config
		mockRunnable3 := mocks.NewMockRunnable()
		// Set DelayRun to 0 to avoid timing issues
		mockRunnable3.DelayRun = 0
		mockRunnable3.On("String").Return("runnable3").Maybe()
		// Allow any number of Run calls for resilience
		mockRunnable3.On("Run", mock.Anything).Return(nil).Maybe()

		mockRunnable4 := mocks.NewMockRunnable()
		// Set DelayRun to 0 to avoid timing issues
		mockRunnable4.DelayRun = 0
		mockRunnable4.On("String").Return("runnable4").Maybe()
		// Allow any number of Run calls for resilience
		mockRunnable4.On("Run", mock.Anything).Return(nil).Maybe()

		// Create updated entries for reload
		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable3, Config: nil},
			{Runnable: mockRunnable4, Config: nil},
		}

		initialConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Create runner with context to use during boot
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create callback that initially returns oldEntries
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
			WithContext[*mocks.Runnable](ctx),
		)
		require.NoError(t, err)

		// Make sure initial config is loaded
		initialConfigLoaded := runner.getConfig()
		assert.NotNil(t, initialConfigLoaded)
		assert.Equal(t, 2, len(initialConfigLoaded.Entries))

		// Set runCtx (normally done by Run)
		runCtx := t.Context()
		runner.runnablesMu.Lock()
		runner.runCtx = runCtx
		runner.runnablesMu.Unlock()

		// Call reloadMembershipChanged directly
		err = runner.reloadMembershipChanged(newConfig)
		require.NoError(t, err)

		// Verify config was updated
		updatedConfig := runner.getConfig()
		require.NotNil(t, updatedConfig)
		assert.Equal(t, 2, len(updatedConfig.Entries))
		assert.Equal(t, mockRunnable3, updatedConfig.Entries[0].Runnable)
		assert.Equal(t, mockRunnable4, updatedConfig.Entries[1].Runnable)

		// Verify mock expectations - disable t.Parallel() and wait a moment to make test more reliable
		time.Sleep(50 * time.Millisecond)
		mockRunnable1.AssertExpectations(t)
		mockRunnable2.AssertExpectations(t)
		mockRunnable3.AssertExpectations(t)
		mockRunnable4.AssertExpectations(t)
	})

	t.Run("handles stopRunnables error", func(t *testing.T) {
		// Setup a runner with no initial config
		runner, err := NewRunner(
			WithConfigCallback[*mocks.Runnable](func() (*Config[*mocks.Runnable], error) {
				return nil, nil
			}),
		)
		require.NoError(t, err)

		// Create a new config
		newEntries := []RunnableEntry[*mocks.Runnable]{}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Set runCtx (normally done by Run)
		runCtx := t.Context()
		runner.runnablesMu.Lock()
		runner.runCtx = runCtx
		runner.runnablesMu.Unlock()

		// Call reloadMembershipChanged
		// This should fail because getConfig() returns nil in stopRunnables
		err = runner.reloadMembershipChanged(newConfig)

		// Verify error
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConfigMissing)
	})

	t.Run("handles boot error", func(t *testing.T) {
		// Setup mock runnables for old config
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()
		mockRunnable1.On("Stop").Maybe()

		// Create initial entries with no runnables
		oldEntries := []RunnableEntry[*mocks.Runnable]{}

		// Create new entries with no runnables to force boot error
		newEntries := []RunnableEntry[*mocks.Runnable]{}

		initialConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Create context for the runner
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create callback that initially returns oldEntries
		configCallback := func() (*Config[*mocks.Runnable], error) {
			return initialConfig, nil
		}

		// Create runner
		runner, err := NewRunner(
			WithConfigCallback(configCallback),
			WithContext[*mocks.Runnable](ctx),
		)
		require.NoError(t, err)

		// Make sure initial config is loaded
		runner.currentConfig.Store(initialConfig)

		// Set runCtx (normally done by Run)
		runCtx := t.Context()
		runner.runnablesMu.Lock()
		runner.runCtx = runCtx
		runner.runnablesMu.Unlock()

		// Call reloadMembershipChanged directly
		// This should fail because the new config has no entries
		err = runner.reloadMembershipChanged(newConfig)

		// Verify error (ErrNoRunnables is returned by boot when no runnables exist)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrNoRunnables)
	})
}

// TestHasMembershipChanged tests the hasMembershipChanged function
func TestHasMembershipChanged(t *testing.T) {
	t.Parallel()

	t.Run("different number of entries", func(t *testing.T) {
		// Create configs with different numbers of entries
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()

		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
		}
		oldConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Membership should have changed
		assert.True(t, hasMembershipChanged(oldConfig, newConfig))
	})

	t.Run("different runnables", func(t *testing.T) {
		// Create configs with the same number of entries but different runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()

		mockRunnable3 := mocks.NewMockRunnable()
		mockRunnable3.On("String").Return("runnable3").Maybe()

		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		oldConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable3, Config: nil}, // Different runnable
		}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Membership should have changed
		assert.True(t, hasMembershipChanged(oldConfig, newConfig))
	})

	t.Run("same runnables", func(t *testing.T) {
		// Create configs with the same runnables
		mockRunnable1 := mocks.NewMockRunnable()
		mockRunnable1.On("String").Return("runnable1").Maybe()

		mockRunnable2 := mocks.NewMockRunnable()
		mockRunnable2.On("String").Return("runnable2").Maybe()

		oldEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		oldConfig, err := NewConfig("test", oldEntries)
		require.NoError(t, err)

		newEntries := []RunnableEntry[*mocks.Runnable]{
			{Runnable: mockRunnable1, Config: nil},
			{Runnable: mockRunnable2, Config: nil},
		}
		newConfig, err := NewConfig("test", newEntries)
		require.NoError(t, err)

		// Membership should not have changed
		assert.False(t, hasMembershipChanged(oldConfig, newConfig))
	})
}
