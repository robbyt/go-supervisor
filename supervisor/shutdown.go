package supervisor

import "sync"

// startShutdownManager starts goroutines to listen for shutdown notifications
// from any runnables that implement ShutdownSender. It blocks until the context is done.
func (p *PIDZero) startShutdownManager() {
	defer p.wg.Done()
	p.logger.Debug("Starting shutdown manager...")

	var shutdownWg sync.WaitGroup

	// Find all runnables that can send shutdown signals and start listeners
	for _, r := range p.runnables {
		if sdSender, ok := r.(ShutdownSender); ok {
			shutdownWg.Add(1)
			// Pass both Runnable 'r' and ShutdownSender 's' for clarity
			go func(r Runnable, s ShutdownSender) {
				defer shutdownWg.Done()
				triggerChan := s.GetShutdownTrigger()
				for {
					select {
					case <-p.ctx.Done():
						return
					case <-triggerChan:
						p.logger.Info("Shutdown requested by runnable", "runnable", r)
						p.Shutdown() // Trigger supervisor shutdown
						return       // Exit this goroutine after triggering shutdown
					}
				}
			}(r, sdSender) // Pass both variables
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
