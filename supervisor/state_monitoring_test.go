package supervisor

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPIDZero_StartStateMonitor tests that the state monitor is started for stateable runnables.
// This test calls pid0.Run() which uses signal.Notify, so it cannot use synctest.
func TestPIDZero_StartStateMonitor(t *testing.T) {
	t.Parallel()

	mockStateable := mocks.NewMockRunnableWithStateable()
	mockStateable.On("String").Return("stateable-runnable").Maybe()
	mockStateable.On("Run", mock.Anything).Return(nil)
	mockStateable.On("Stop").Once()
	mockStateable.On("GetState").Return("initial").Once()
	mockStateable.On("GetState").Return("running").Maybe()

	stateChan := make(chan string, 5)
	mockStateable.On("GetStateChan", mock.Anything).Return(stateChan).Once()

	mockStateable.On("IsRunning").Return(true).Once()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pid0, err := New(
		WithContext(ctx),
		WithRunnables(mockStateable),
		WithStartupTimeout(100*time.Millisecond),
	)
	require.NoError(t, err)

	stateUpdates := make(chan StateMap, 5)
	unsubscribe := pid0.AddStateSubscriber(stateUpdates)
	defer unsubscribe()

	execDone := make(chan error, 1)
	go func() {
		execDone <- pid0.Run()
	}()

	stateChan <- "initial"
	stateChan <- "running"
	stateChan <- "stopping"

	require.Eventually(t, func() bool {
		select {
		case stateMap := <-stateUpdates:
			return stateMap["stateable-runnable"] != ""
		default:
			return false
		}
	}, 500*time.Millisecond, 50*time.Millisecond, "No state updates received")

	cancel()

	select {
	case err := <-execDone:
		require.NoError(t, err)
	case <-time.After(1 * time.Second):
		t.Fatal("Supervisor did not shut down in time")
	}

	mockStateable.AssertExpectations(t)
}

// TestPIDZero_SubscribeStateChanges tests the SubscribeStateChanges functionality.
func TestPIDZero_SubscribeStateChanges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		mockService := mocks.NewMockRunnableWithStateable()
		stateChan := make(chan string, 2)
		mockService.On("GetStateChan", mock.Anything).Return(stateChan).Once()
		mockService.On("String").Return("mock-service").Maybe()
		mockService.On("Run", mock.Anything).Return(nil).Maybe()
		mockService.On("Stop").Maybe()
		mockService.On("GetState").Return("initial").Maybe()
		mockService.On("IsRunning").Return(true).Maybe()

		pid0, err := New(WithContext(ctx), WithRunnables(mockService))
		require.NoError(t, err)

		pid0.stateMap.Store(mockService, "initial")

		subCtx, subCancel := context.WithCancel(t.Context())
		defer subCancel()
		stateMapChan := pid0.SubscribeStateChanges(subCtx)

		pid0.wg.Go(pid0.startStateMonitor)
		synctest.Wait()

		stateChan <- "initial"
		stateChan <- "running"

		pid0.stateMap.Store(mockService, "running")
		pid0.broadcastState()

		synctest.Wait()

		var foundRunning bool
		for range len(stateMapChan) {
			stateMap := <-stateMapChan
			if val, ok := stateMap["mock-service"]; ok && val == "running" {
				foundRunning = true
				break
			}
		}
		assert.True(t, foundRunning, "Should have received a state map with running state")

		cancel()

		mockService.AssertExpectations(t)
	})
}
