package lifecycle

import "sync"

// closedDoneCh is returned by DoneCh() when Started() has not been called.
// Pre-Run state is treated the same as post-Run for callers: there is no
// live Run() goroutine, so callers selecting on DoneCh() should abort
// rather than send into runner-internal channels with no reader.
var closedDoneCh = func() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}()

// StartStop manages the Run/Stop synchronization for a Runnable.
// It ensures Stop() blocks until Run() has completed, handling all
// orderings: stop-before-run, stop-during-run, stop-after-run,
// and multiple concurrent Stop() calls. It supports reuse across
// multiple Run/Stop cycles.
type StartStop struct {
	mu        sync.Mutex
	stopCh    chan struct{}
	startedCh chan struct{}
	doneCh    chan struct{}
	stopped   bool
}

// New creates a new StartStop instance.
func New() *StartStop {
	return &StartStop{
		stopCh:    make(chan struct{}),
		startedCh: make(chan struct{}),
	}
}

// Started is called at the beginning of Run(). It returns a done function
// that must be deferred to signal Run() completion. If a previous Run/Stop
// cycle has completed, the lifecycle is reset for reuse.
func (l *StartStop) Started() (done func()) {
	doneCh := make(chan struct{})

	l.mu.Lock()
	if l.doneCh != nil {
		select {
		case <-l.doneCh:
			l.stopCh = make(chan struct{})
			l.startedCh = make(chan struct{})
			l.stopped = false
		default:
		}
	}
	l.doneCh = doneCh

	select {
	case <-l.startedCh:
	default:
		close(l.startedCh)
	}
	l.mu.Unlock()

	var doneOnce sync.Once
	return func() { doneOnce.Do(func() { close(doneCh) }) }
}

// Stop signals the Runnable to stop and blocks until Run() completes.
// If Run() has not been called yet, Stop blocks until it starts and finishes.
// Safe to call from multiple goroutines concurrently.
func (l *StartStop) Stop() {
	l.mu.Lock()
	if !l.stopped {
		l.stopped = true
		close(l.stopCh)
	}
	startedCh := l.startedCh
	l.mu.Unlock()

	<-startedCh

	l.mu.Lock()
	doneCh := l.doneCh
	l.mu.Unlock()

	<-doneCh
}

// StopCh returns a channel that is closed when Stop() is called.
// Use this in a select statement within Run() to detect stop signals.
func (l *StartStop) StopCh() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopCh
}

// DoneCh returns a channel that is closed when Run() has exited (the done
// func returned by Started has been called). If Started has not yet been
// called, DoneCh returns an already-closed channel: callers should treat
// pre-Run identically to post-Run, since neither state has a live Run()
// goroutine to receive from runner-internal channels.
//
// Use this to avoid sending into a buffered runner channel that no
// goroutine will drain, e.g. a reload-request channel after Run has
// finished its select loop.
func (l *StartStop) DoneCh() <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.doneCh == nil {
		return closedDoneCh
	}
	return l.doneCh
}
