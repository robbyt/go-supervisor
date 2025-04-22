package composite

import (
	"context"
	"fmt"
	"sync"

	"github.com/robbyt/go-supervisor/internal/finitestate"
)

// GetState returns the current state of the CompositeRunner.
func (r *CompositeRunner[T]) GetState() string {
	return r.fsm.GetState()
}

// GetStateChan returns a channel that will receive state updates.
func (r *CompositeRunner[T]) GetStateChan(ctx context.Context) <-chan string {
	return r.fsm.GetStateChan(ctx)
}

// setStateError marks the FSM as being in the error state.
func (r *CompositeRunner[T]) setStateError() {
	err := r.fsm.SetState(finitestate.StatusError)
	if err != nil {
		r.logger.Error("Failed to transition to Error state", "error", err)
	}
}

// Run starts all child runnables in order (first to last) and monitors for completion or errors.
// This method blocks until all child runnables are stopped or an error occurs.
func (r *CompositeRunner[T]) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Transition from New to Booting
	if err := r.fsm.Transition(finitestate.StatusBooting); err != nil {
		return fmt.Errorf("failed to transition to Booting state: %w", err)
	}

	// Start all child runnables
	if err := r.boot(); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to start child runnables: %w", err)
	}

	// Transition from Booting to Running
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to Running state: %w", err)
	}

	// Wait for context cancellation or errors
	select {
	case <-runCtx.Done():
		r.logger.Debug("Local context canceled")
	case <-r.ctx.Done():
		r.logger.Debug("Parent context canceled")
	case err := <-r.serverErrors:
		r.setStateError()
		return fmt.Errorf("%w: %w", ErrRunnableFailed, err)
	}

	// Try to transition to Stopping state
	if !r.fsm.TransitionBool(finitestate.StatusStopping) {
		if r.fsm.GetState() == finitestate.StatusStopping {
			r.logger.Debug("Already in Stopping state, continuing shutdown")
		} else {
			r.setStateError()
			return fmt.Errorf("failed to transition to Stopping state")
		}
	}

	// Stop all child runnables
	if err := r.stopRunnables(); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to stop runnables: %w", err)
	}

	// Transition to Stopped state
	if err := r.fsm.Transition(finitestate.StatusStopped); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to Stopped state: %w", err)
	}

	r.logger.Debug("All child runnables shut down gracefully")
	return nil
}

// Stop will cancel the parent context, causing all child runnables to stop.
func (r *CompositeRunner[T]) Stop() {
	// Only transition to Stopping if we're currently Running
	if err := r.fsm.TransitionIfCurrentState(finitestate.StatusRunning, finitestate.StatusStopping); err != nil {
		// This error is expected if we're already stopping, so only log at debug level
		r.logger.Debug("Not transitioning to Stopping state", "error", err)
	}
	r.cancel()
}

// boot starts all child runnables in the order they're defined.
func (r *CompositeRunner[T]) boot() error {
	// Get read lock while starting runnables
	r.runnablesMu.RLock()
	defer r.runnablesMu.RUnlock()

	cfg := r.getConfig()
	if cfg == nil {
		return ErrConfigMissing
	}

	if len(cfg.Entries) == 0 {
		return ErrNoRunnables
	}

	// Start each runnable, tracking which ones have been started
	started := make([]T, 0, len(cfg.Entries))

	for i, entry := range cfg.Entries {
		r.logger.Debug("Starting child runnable", "index", i, "runnable", entry.Runnable)

		// Start the runnable in a goroutine
		errCh := make(chan error, 1)
		go func(run T) {
			err := run.Run(r.ctx)
			if err != nil {
				errCh <- err
			}
			close(errCh)
		}(entry.Runnable)

		// Add to list of started runnables
		started = append(started, entry.Runnable)

		// Check if the runnable failed to start immediately
		select {
		case err := <-errCh:
			if err != nil {
				// Stop all runnables that were started
				r.stopStartedRunnables(started[:len(started)-1])
				return fmt.Errorf("failed to start runnable %s: %w", entry.Runnable, err)
			}
		default:
			// No immediate error, continue
		}
	}

	// Start a goroutine to monitor for late failures
	for i, entry := range cfg.Entries {
		go func(idx int, run T) {
			err := run.Run(r.ctx)
			if err != nil {
				r.logger.Error("Child runnable failed", "index", idx, "runnable", run, "error", err)
				select {
				case r.serverErrors <- fmt.Errorf("runnable %s failed: %w", run, err):
				default:
					// Channel full, which means we've already reported an error
				}
			}
		}(i, entry.Runnable)
	}

	return nil
}

// stopStartedRunnables stops all runnables that were successfully started,
// in reverse order from how they were started.
func (r *CompositeRunner[T]) stopStartedRunnables(started []T) {
	for i := len(started) - 1; i >= 0; i-- {
		runner := started[i]
		r.logger.Debug("Stopping child runnable during startup failure", "runnable", runner)
		runner.Stop()
	}
}

// stopRunnables stops all child runnables in reverse order (last to first).
func (r *CompositeRunner[T]) stopRunnables() error {
	// Get read lock while stopping runnables
	r.runnablesMu.RLock()
	defer r.runnablesMu.RUnlock()

	cfg := r.getConfig()
	if cfg == nil {
		return ErrConfigMissing
	}

	// Create a WaitGroup to wait for all runnables to complete their Stop call
	var wg sync.WaitGroup
	wg.Add(len(cfg.Entries))

	// Stop each runnable in reverse order
	for i := len(cfg.Entries) - 1; i >= 0; i-- {
		entry := cfg.Entries[i]
		r.logger.Debug("Stopping child runnable", "index", i, "runnable", entry.Runnable)

		go func(run T) {
			run.Stop()
			wg.Done()
		}(entry.Runnable)
	}

	// Wait for all runnables to complete stopping
	wg.Wait()
	return nil
}
