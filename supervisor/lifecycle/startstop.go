package lifecycle

import "sync"

// StartStop manages the Run/Stop synchronization for a Runnable.
// It ensures Stop() blocks until Run() has completed, handling all
// orderings: stop-before-run, stop-during-run, stop-after-run,
// and multiple concurrent Stop() calls.
type StartStop struct {
	mu        sync.Mutex
	stopOnce  sync.Once
	startOnce sync.Once
	stopCh    chan struct{}
	startedCh chan struct{}
	doneCh    chan struct{}
}

// New creates a new StartStop instance.
func New() *StartStop {
	return &StartStop{
		stopCh:    make(chan struct{}),
		startedCh: make(chan struct{}),
	}
}

// Started is called at the beginning of Run(). It returns a done function
// that must be deferred to signal Run() completion.
func (l *StartStop) Started() (done func()) {
	doneCh := make(chan struct{})
	l.mu.Lock()
	l.doneCh = doneCh
	l.mu.Unlock()
	l.startOnce.Do(func() { close(l.startedCh) })
	var doneOnce sync.Once
	return func() { doneOnce.Do(func() { close(doneCh) }) }
}

// Stop signals the Runnable to stop and blocks until Run() completes.
// If Run() has not been called yet, Stop blocks until it starts and finishes.
// Safe to call from multiple goroutines concurrently.
func (l *StartStop) Stop() {
	l.stopOnce.Do(func() { close(l.stopCh) })
	<-l.startedCh
	l.mu.Lock()
	doneCh := l.doneCh
	l.mu.Unlock()
	<-doneCh
}

// StopCh returns a channel that is closed when Stop() is called.
// Use this in a select statement within Run() to detect stop signals.
func (l *StartStop) StopCh() <-chan struct{} {
	return l.stopCh
}
