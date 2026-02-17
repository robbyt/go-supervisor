package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/runnables/composite"
	"github.com/robbyt/go-supervisor/supervisor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorker wraps Worker to track Stop calls via a callback, using a stable ID.
type TestWorker struct {
	*Worker                 // Embed the actual Worker implementation by pointer
	id      string          // Use a separate ID field, distinct from Worker.name which can change
	onStop  func(id string) // Callback uses the distinct ID
	t       *testing.T      // Store the testing.T pointer
}

// NewTestWorker creates a TestWorker instance.
func NewTestWorker(
	t *testing.T,
	config WorkerConfig,
	logger *slog.Logger,
	onStop func(id string),
) (*TestWorker, error) {
	t.Helper()
	worker, err := NewWorker(config, logger) // Handle error from NewWorker
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded worker for %s: %w", config.JobName, err)
	}
	return &TestWorker{
		Worker: worker,
		id:     config.JobName,
		onStop: onStop,
		t:      t, // Store the testing.T pointer
	}, nil
}

// Override Stop to call the onStop callback with the TestWorker's ID
// before calling the embedded Worker's Stop method.
func (w *TestWorker) Stop() {
	// Add defensive nil checks
	if w == nil {
		return
	}

	// Use the testing.T if available
	if w.t != nil {
		w.t.Helper()
	}

	// Add debug log message to help trace calls
	if w.id != "" {
		fmt.Printf("TestWorker Stop called for ID: %s\n", w.id)
	}

	// Call the callback first, using the TestWorker's stable 'id'.
	if w.onStop != nil && w.id != "" {
		w.onStop(w.id)
	} else if w.id != "" {
		fmt.Printf("WARNING: TestWorker %s has nil onStop callback\n", w.id)
	}

	// Delegate to the embedded Worker's Stop method.
	if w.Worker != nil {
		w.Worker.Stop()
	} else if w.id != "" {
		fmt.Printf("WARNING: TestWorker %s has nil Worker\n", w.id)
	}
}

// Ensure TestWorker still satisfies the composite.runnable interface
var (
	_ supervisor.Runnable            = (*TestWorker)(nil)
	_ composite.ReloadableWithConfig = (*TestWorker)(nil)
)

// TestMembershipChangesBasic is a simplified, basic test to verify the CompositeRunner
// properly adds and removes runnables during reload.
func TestMembershipChangesBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}

	logger, _ := testLogger(t, true)

	// Channel for tracking stopped workers
	stoppedCh := make(chan string, 10)
	stoppedWorkers := make(map[string]bool)

	// Mutex to protect access to stoppedWorkers map from multiple goroutines
	var stoppedMu sync.Mutex

	// Create a callback that records stopped workers
	onStopCallback := func(id string) {
		stoppedMu.Lock()
		defer stoppedMu.Unlock()
		stoppedWorkers[id] = true
		stoppedCh <- id // Keep the channel functionality for later checks
		t.Logf("Worker stopped: %s", id)
	}

	worker1, err := NewTestWorker(t,
		WorkerConfig{
			JobName:  "worker1",
			Interval: 100 * time.Millisecond,
		},
		logger, onStopCallback)
	require.NoError(t, err)

	worker2, err := NewTestWorker(t,
		WorkerConfig{
			JobName:  "worker2",
			Interval: 150 * time.Millisecond,
		},
		logger, onStopCallback)
	require.NoError(t, err)

	// Initial configuration with two workers
	configEntries := []composite.RunnableEntry[*TestWorker]{
		{Runnable: worker1, Config: worker1.config},
		{Runnable: worker2, Config: worker2.config},
	}

	// Create config callback that returns the current entries
	configCh := make(chan []composite.RunnableEntry[*TestWorker], 1)
	configCh <- configEntries // Initial configuration

	configCallback := func() (*composite.Config[*TestWorker], error) {
		entries := <-configCh // Get current config
		configCh <- entries   // Put it back for next caller
		return composite.NewConfig("test-runner", entries)
	}

	// Create composite runner with reasonable timeout
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	runner, err := composite.NewRunner[*TestWorker](
		configCallback,
		composite.WithLogHandler[*TestWorker](logger.Handler()),
	)
	require.NoError(t, err)

	// Start runner in a goroutine
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runner.Run(ctx)
	}()

	// Wait for initial configuration to be applied
	assert.Eventually(t, func() bool {
		return runner.IsRunning()
	}, 1*time.Second, 10*time.Millisecond)

	// --- Test Step 1: Remove worker1, add worker3 ---
	worker3, err := NewTestWorker(t,
		WorkerConfig{
			JobName:  "worker3",
			Interval: 50 * time.Millisecond,
		},
		logger, onStopCallback)
	require.NoError(t, err)

	// Update to new config: remove worker1, keep worker2, add worker3
	updatedEntries := []composite.RunnableEntry[*TestWorker]{
		{Runnable: worker2, Config: worker2.config},
		{Runnable: worker3, Config: worker3.config},
	}

	// Update configuration
	<-configCh                 // Remove old config (discard it)
	configCh <- updatedEntries // Set new config

	// Trigger reload
	t.Log("Triggering reload to remove worker1 and add worker3")
	runner.Reload(t.Context())

	// Use assert.Eventually to check that worker1 was stopped
	assert.Eventually(t, func() bool {
		stoppedMu.Lock()
		defer stoppedMu.Unlock()
		return stoppedWorkers["worker1"]
	}, 1*time.Second, 10*time.Millisecond, "Worker1 should be stopped during reload")

	// --- Cleanup ---
	t.Log("Cancelling context to stop all workers")
	cancel()

	// Wait for runner to finish
	select {
	case err := <-doneCh:
		if err != nil && !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Runner exited with unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for runner to exit")
	}

	// Use assert.Eventually to verify the remaining workers are stopped
	assert.Eventually(t, func() bool {
		stoppedMu.Lock()
		defer stoppedMu.Unlock()
		return stoppedWorkers["worker2"] && stoppedWorkers["worker3"]
	}, 1*time.Second, 10*time.Millisecond, "All workers should be stopped during cleanup")

	// Log final state for debugging
	stoppedMu.Lock()
	t.Logf("Final stopped workers: %v", stoppedWorkers)
	stoppedMu.Unlock()
}
