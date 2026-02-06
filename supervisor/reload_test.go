package supervisor

import (
	"context"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestPIDZero_ReloadManager tests the reload manager functionality.
func TestPIDZero_ReloadManager(t *testing.T) {
	t.Run("handles reload notifications", func(t *testing.T) {
		// Setup mock
		sender := mocks.NewMockRunnableWithReloadSender()
		reloadTrigger := make(chan struct{})
		stateChan := make(chan string)

		sender.On("GetReloadTrigger").Return(reloadTrigger)
		sender.On("Run", mock.Anything).Return(nil)
		sender.On("Reload").Return()
		sender.On("Stop").Return()
		sender.On("GetState").Return("running").Maybe()
		sender.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

		p, err := New(WithContext(context.Background()), WithRunnables(sender))
		require.NoError(t, err)

		done := make(chan struct{})
		go func() {
			err := p.Run()
			assert.NoError(t, err)
			close(done)
		}()

		// Trigger reload
		reloadTrigger <- struct{}{}

		// Allow reload to process
		time.Sleep(100 * time.Millisecond)

		// Verify reload was called once
		sender.AssertCalled(t, "Reload")
		sender.AssertNumberOfCalls(t, "Reload", 1)

		p.Shutdown()
		<-done

		sender.AssertExpectations(t)
	})

	t.Run("handles multiple reloads", func(t *testing.T) {
		sender1 := mocks.NewMockRunnableWithReloadSender()
		sender2 := mocks.NewMockRunnableWithReloadSender()

		reloadTrigger1 := make(chan struct{})
		reloadTrigger2 := make(chan struct{})
		stateChan1 := make(chan string)
		stateChan2 := make(chan string)

		sender1.On("GetReloadTrigger").Return(reloadTrigger1)
		sender2.On("GetReloadTrigger").Return(reloadTrigger2)

		sender1.On("Run", mock.Anything).Return(nil)
		sender2.On("Run", mock.Anything).Return(nil)

		sender1.On("Reload").Return()
		sender2.On("Reload").Return()

		sender1.On("Stop").Return()
		sender2.On("Stop").Return()

		sender1.On("GetState").Return("running").Maybe()
		sender2.On("GetState").Return("running").Maybe()
		sender1.On("GetStateChan", mock.Anything).Return(stateChan1).Maybe()
		sender2.On("GetStateChan", mock.Anything).Return(stateChan2).Maybe()

		p, err := New(WithContext(context.Background()), WithRunnables(sender1, sender2))
		require.NoError(t, err)

		done := make(chan struct{})
		go func() {
			err := p.Run()
			assert.NoError(t, err)
			close(done)
		}()

		// Two triggers => each service should reload twice
		reloadTrigger1 <- struct{}{}
		reloadTrigger2 <- struct{}{}

		time.Sleep(100 * time.Millisecond)

		// Expect each service to have Reload() called twice
		sender1.AssertNumberOfCalls(t, "Reload", 2)
		sender2.AssertNumberOfCalls(t, "Reload", 2)

		p.Shutdown()
		<-done

		sender1.AssertExpectations(t)
		sender2.AssertExpectations(t)
	})

	t.Run("graceful shutdown", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		sender := mocks.NewMockRunnableWithReloadSender()
		reloadTrigger := make(chan struct{})
		stateChan := make(chan string)

		sender.On("GetReloadTrigger").Return(reloadTrigger)
		sender.On("Run", mock.Anything).Return(nil)
		sender.On("Stop").Return()
		sender.On("GetState").Return("running").Maybe()
		sender.On("GetStateChan", mock.Anything).Return(stateChan).Maybe()

		p, err := New(WithContext(ctx), WithRunnables(sender))
		require.NoError(t, err)

		done := make(chan struct{})
		go func() {
			err := p.Run()
			assert.NoError(t, err)
			close(done)
		}()

		// Cancel context to trigger shutdown
		cancel()

		select {
		case <-done:
			sender.AssertCalled(t, "Stop")
		case <-time.After(time.Second):
			t.Fatal("shutdown timed out")
		}

		sender.AssertExpectations(t)
	})

	// The test for state monitoring is covered in getState_test.go

	t.Run("manual ReloadAll call", func(t *testing.T) {
		// Setup multiple reloadable services that aren't reload senders
		mockService1 := mocks.NewMockRunnable()
		mockService2 := mocks.NewMockRunnable()

		mockService1.On("String").Return("ReloadableService1").Maybe()
		mockService2.On("String").Return("ReloadableService2").Maybe()

		mockService1.On("Reload").Once()
		mockService2.On("Reload").Once()

		mockService1.On("Run", mock.Anything).Return(nil).Once()
		mockService2.On("Run", mock.Anything).Return(nil).Once()

		mockService1.On("Stop").Once()
		mockService2.On("Stop").Once()

		// Create supervisor with both services
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		pid0, err := New(WithContext(ctx), WithRunnables(mockService1, mockService2))
		require.NoError(t, err)

		// Start supervisor
		execDone := make(chan error, 1)
		go func() {
			execDone <- pid0.Run()
		}()

		// Allow time for services to start
		time.Sleep(10 * time.Millisecond)

		// Manually trigger reload via API call
		pid0.ReloadAll()

		// Allow time for reload to complete
		time.Sleep(50 * time.Millisecond)

		// Verify both services were reloaded
		mockService1.AssertNumberOfCalls(t, "Reload", 1)
		mockService2.AssertNumberOfCalls(t, "Reload", 1)

		// Shutdown and wait for completion
		pid0.Shutdown()
		select {
		case err := <-execDone:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("shutdown timed out")
		}

		// Verify expectations
		mockService1.AssertExpectations(t)
		mockService2.AssertExpectations(t)
	})

	t.Run("SIGTERM handled while reload is in progress", func(t *testing.T) {
		mockService := mocks.NewMockRunnable()
		mockService.On("String").Return("SlowReloader").Maybe()
		mockService.On("Run", mock.Anything).Return(nil)
		mockService.On("Stop").Return()
		mockService.DelayReload = 500 * time.Millisecond
		mockService.On("Reload").Return()

		pid0, err := New(WithContext(context.Background()), WithRunnables(mockService))
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() {
			done <- pid0.Run()
		}()

		require.Eventually(t, func() bool {
			return pid0.ctx.Err() == nil
		}, time.Second, 5*time.Millisecond)

		pid0.ReloadAll()

		// Send SIGTERM while reload is in progress - the reap loop must stay responsive
		pid0.SignalChan <- syscall.SIGTERM

		require.Eventually(t, func() bool {
			select {
			case err := <-done:
				assert.NoError(t, err)
				return true
			default:
				return false
			}
		}, 2*time.Second, 10*time.Millisecond, "SIGTERM was not handled while reload was in progress")

		mockService.AssertCalled(t, "Stop")
	})

	t.Run("concurrent ReloadAll is safe under race detector", func(t *testing.T) {
		mockService := mocks.NewMockRunnable()
		mockService.On("String").Return("ConcurrentReloader").Maybe()
		mockService.On("Run", mock.Anything).Return(nil)
		mockService.On("Stop").Return()
		mockService.On("Reload").Return()

		pid0, err := New(WithContext(context.Background()), WithRunnables(mockService))
		require.NoError(t, err)

		done := make(chan error, 1)
		go func() {
			done <- pid0.Run()
		}()

		require.Eventually(t, func() bool {
			return pid0.ctx.Err() == nil
		}, time.Second, 5*time.Millisecond)

		var wg sync.WaitGroup
		var callCount atomic.Int32
		for range 10 {
			wg.Go(func() {
				pid0.ReloadAll()
				callCount.Add(1)
			})
		}
		wg.Wait()
		assert.Equal(t, int32(10), callCount.Load(), "all ReloadAll calls should return")

		pid0.Shutdown()
		require.Eventually(t, func() bool {
			select {
			case err := <-done:
				assert.NoError(t, err)
				return true
			default:
				return false
			}
		}, 2*time.Second, 10*time.Millisecond, "shutdown timed out")
	})
}
