package supervisor

import "sync"

// startShutdownManager starts goroutines to listen for shutdown notifications
// from any runnables that implement ShutdownSender. It blocks until the context is done.
func (p *PIDZero) startShutdownManager() {
	p.logger.Debug("Starting shutdown manager...")

	var shutdownWg sync.WaitGroup

	// Find all runnables that can send shutdown signals and start listeners
	for _, r := range p.runnables {
		if sdSender, ok := r.(ShutdownSender); ok {
			shutdownWg.Add(1)
			go func(r Runnable, s ShutdownSender) {
				defer shutdownWg.Done()
				triggerChan := s.GetShutdownTrigger()
				select {
				case <-p.ctx.Done():
				case <-triggerChan:
					p.logger.Info("Shutdown requested by runnable", "runnable", r)
					// Dispatch via ctx cancellation; reap's existing
					// <-p.ctx.Done() handler invokes Shutdown. The
					// listener stays a notifier — it doesn't run
					// Shutdown itself, which is what previously created
					// a circular wait (listener inside Shutdown's
					// p.wg.Wait while startShutdownManager waited on
					// the listener via shutdownWg.Wait).
					p.cancel()
				}
			}(r, sdSender)
		}
	}

	// Block until context is done, then wait for listener goroutines to finish
	<-p.ctx.Done()
	p.logger.Debug(
		"Shutdown manager received context done signal, waiting for listeners to exit...",
	)
	shutdownWg.Wait()
	p.logger.Debug("Shutdown manager complete.")
}
