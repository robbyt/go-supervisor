package composite

import (
	"context"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestIntegration_CompositeNoRaceCondition verifies that when composite runner
// reports as running, all child runnables are actually running.
// This tests the real FSM implementation, not mocks.
func TestIntegration_CompositeNoRaceCondition(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	const iterations = 5

	for i := 0; i < iterations; i++ {
		t.Run(fmt.Sprintf("iteration_%d", i), func(t *testing.T) {
			testCompositeRaceCondition(t)
		})
	}
}

func testCompositeRaceCondition(t *testing.T) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		runCalled := make(chan struct{}, 3)

		mock1 := mocks.NewMockRunnableWithStateable()
		mock2 := mocks.NewMockRunnableWithStateable()
		mock3 := mocks.NewMockRunnableWithStateable()
		mock1.On("String").Return("service1")
		mock1.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			runCalled <- struct{}{}
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		})
		mock1.On("Stop").Return()
		mock1.On("IsRunning").Return(true)
		mock1.On("GetState").Return("Running")

		mock2.On("String").Return("service2")
		mock2.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			runCalled <- struct{}{}
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		})
		mock2.On("Stop").Return()
		mock2.On("IsRunning").Return(true)
		mock2.On("GetState").Return("Running")

		mock3.On("String").Return("service3")
		mock3.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			runCalled <- struct{}{}
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		})
		mock3.On("Stop").Return()
		mock3.On("IsRunning").Return(true)
		mock3.On("GetState").Return("Running")

		callback := func() (*Config[*mocks.MockRunnableWithStateable], error) {
			return NewConfig("test-composite", []RunnableEntry[*mocks.MockRunnableWithStateable]{
				{Runnable: mock1},
				{Runnable: mock2},
				{Runnable: mock3},
			})
		}

		runner, err := NewRunner(callback)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		// Advance virtual clock past the mock's default 1ms Run delay
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		assert.True(t, runner.IsRunning(), "Composite should report as running")
		assert.Len(t, runCalled, 3, "All 3 Run() methods should have been called")

		assert.True(t, mock1.IsRunning(),
			"RACE CONDITION: Composite reports running but child 1 not running")
		assert.True(t, mock2.IsRunning(),
			"RACE CONDITION: Composite reports running but child 2 not running")
		assert.True(t, mock3.IsRunning(),
			"RACE CONDITION: Composite reports running but child 3 not running")

		childStates := runner.GetChildStates()
		assert.Len(t, childStates, 3, "Should have 3 child states")
		for name, state := range childStates {
			assert.Equal(t, "Running", state, "Child %s should be running", name)
		}

		cancel()
		synctest.Wait()

		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Composite did not shutdown within timeout")
		}

		mock1.AssertCalled(t, "Stop")
		mock2.AssertCalled(t, "Stop")
		mock3.AssertCalled(t, "Stop")
	})
}

// TestIntegration_CompositeFullLifecycle tests complete composite lifecycle
func TestIntegration_CompositeFullLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	synctest.Test(t, func(t *testing.T) {
		mock1 := mocks.NewMockRunnableWithStateable()
		mock2 := mocks.NewMockRunnableWithStateable()

		mock1.On("String").Return("mock-service-1")
		mock1.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		})
		mock1.On("Stop").Return()
		mock1.On("GetState").Return("Running")

		mock2.On("String").Return("mock-service-2")
		mock2.On("Run", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			ctx := args.Get(0).(context.Context)
			<-ctx.Done()
		})
		mock2.On("Stop").Return()
		mock2.On("GetState").Return("Running")

		callback := func() (*Config[*mocks.MockRunnableWithStateable], error) {
			return NewConfig("integration-test", []RunnableEntry[*mocks.MockRunnableWithStateable]{
				{Runnable: mock1},
				{Runnable: mock2},
			})
		}

		runner, err := NewRunner(callback)
		require.NoError(t, err)

		assert.Equal(t, "New", runner.GetState())
		assert.False(t, runner.IsRunning())

		ctx, cancel := context.WithCancel(t.Context())

		runErr := make(chan error, 1)
		go func() {
			runErr <- runner.Run(ctx)
		}()

		// Advance virtual clock past the mock's default 1ms Run delay
		time.Sleep(10 * time.Millisecond)
		synctest.Wait()

		assert.True(t, runner.IsRunning(), "Should be Running")
		assert.Equal(t, "Running", runner.GetState())

		mock1.AssertCalled(t, "Run", mock.Anything)
		mock2.AssertCalled(t, "Run", mock.Anything)

		childStates := runner.GetChildStates()
		assert.Len(t, childStates, 2)
		assert.Equal(t, "Running", childStates["mock-service-1"])
		assert.Equal(t, "Running", childStates["mock-service-2"])

		cancel()
		synctest.Wait()

		select {
		case err := <-runErr:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("Runner did not shutdown within timeout")
		}

		assert.Equal(t, "Stopped", runner.GetState())
		assert.False(t, runner.IsRunning())

		mock1.AssertCalled(t, "Stop")
		mock2.AssertCalled(t, "Stop")
	})
}
