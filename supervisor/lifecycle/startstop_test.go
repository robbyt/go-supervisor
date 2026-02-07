package lifecycle

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStartStop_StopBlocksUntilRunCompletes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lc := New()

		runReturned := atomic.Bool{}
		go func() {
			done := lc.Started()
			defer done()
			<-lc.StopCh()
			runReturned.Store(true)
		}()

		time.Sleep(time.Second)
		synctest.Wait()

		lc.Stop()

		assert.True(t, runReturned.Load(), "Run should have returned before Stop unblocked")
	})
}

func TestStartStop_StopBeforeRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lc := New()

		stopReturned := atomic.Bool{}
		go func() {
			lc.Stop()
			stopReturned.Store(true)
		}()

		time.Sleep(time.Second)
		synctest.Wait()

		assert.False(t, stopReturned.Load(), "Stop should block until Run starts and completes")

		done := lc.Started()
		done()

		time.Sleep(time.Second)
		synctest.Wait()

		assert.True(t, stopReturned.Load(), "Stop should unblock after Run completes")
	})
}

func TestStartStop_StopAfterRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lc := New()

		done := lc.Started()
		done()

		lc.Stop()
	})
}

func TestStartStop_MultipleConcurrentStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lc := New()

		go func() {
			done := lc.Started()
			defer done()
			<-lc.StopCh()
		}()

		time.Sleep(time.Second)
		synctest.Wait()

		var wg sync.WaitGroup
		for range 5 {
			wg.Go(func() {
				lc.Stop()
			})
		}
		wg.Wait()
	})
}

func TestStartStop_DoubleStartedDoesNotPanic(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lc := New()

		done1 := lc.Started()
		done2 := lc.Started() // must not panic
		done1()
		done2()

		lc.Stop()
	})
}

func TestStartStop_DoubleDoneDoesNotPanic(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lc := New()

		done := lc.Started()
		done()
		done() // must not panic

		lc.Stop()
	})
}

func TestStartStop_StopChClosedAfterStop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		lc := New()

		go func() {
			done := lc.Started()
			defer done()
			<-lc.StopCh()
		}()

		time.Sleep(time.Second)
		synctest.Wait()

		lc.Stop()

		select {
		case <-lc.StopCh():
			// expected
		default:
			t.Fatal("StopCh should be closed after Stop()")
		}
	})
}
