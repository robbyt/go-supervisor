package main // Assuming test is in the same package

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	// Use paths from the user's example
	"github.com/robbyt/go-supervisor/runnables/composite"
	"github.com/robbyt/go-supervisor/supervisor"
	// Use testify imports
	"github.com/stretchr/testify/require"
)

// --- TestWorker Definition (Updated) ---

// TestWorker wraps Worker to track Stop calls via a callback, using a stable ID.
type TestWorker struct {
	*Worker                 // Embed the actual Worker implementation by pointer
	id      string          // Use a separate ID field, distinct from Worker.name which can change
	onStop  func(id string) // Callback uses the distinct ID
}

// NewTestWorker creates a TestWorker instance.
func NewTestWorker(
	id string, // Use 'id' for the TestWorker's identifier
	config WorkerConfig,
	logger *slog.Logger,
	onStop func(id string),
) (*TestWorker, error) { // Return error
	// Ensure the JobName in the initial config matches the stable ID.
	// The CompositeRunner might update this later via ReloadWithConfig.
	config.JobName = id

	// Create the underlying Worker
	worker, err := NewWorker(config, logger) // Handle error from NewWorker
	if err != nil {
		return nil, fmt.Errorf("failed to create embedded worker for %s: %w", id, err)
	}

	return &TestWorker{
		Worker: worker,
		id:     id, // Store the distinct ID
		onStop: onStop,
	}, nil
}

// Override Stop to call the onStop callback with the TestWorker's ID
// before calling the embedded Worker's Stop method.
func (tw *TestWorker) Stop() {
	// Add debug log message to help trace calls
	fmt.Printf("TestWorker Stop called for ID: %s\n", tw.id)

	// Call the callback first, using the TestWorker's stable 'id'.
	if tw.onStop != nil {
		tw.onStop(tw.id)
	} else {
		fmt.Printf("WARNING: TestWorker %s has nil onStop callback\n", tw.id)
	}

	// Delegate to the embedded Worker's Stop method.
	if tw.Worker != nil {
		tw.Worker.Stop()
	} else {
		fmt.Printf("WARNING: TestWorker %s has nil Worker\n", tw.id)
	}
}

// Ensure TestWorker still satisfies the composite.runnable interface
var (
	_ supervisor.Runnable            = (*TestWorker)(nil)
	_ composite.ReloadableWithConfig = (*TestWorker)(nil)
)

// --- Test Case (Updated) ---

// TestMembershipChangesBasic is a simplified, basic test to verify the CompositeRunner
// properly adds and removes runnables during reload.
// Note: This test may show race conditions when run with -race flag due to the inherent
// concurrency within composite runner, but it provides a functional verification of
// worker membership changes.
func TestMembershipChangesBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}

	logger, _ := testLogger(t, true)

	// Channel for tracking stopped workers - synchronization via channels instead of maps
	stoppedCh := make(chan string, 10)

	// Create initial workers
	worker1, err := NewTestWorker("worker1", WorkerConfig{
		Interval: 100 * time.Millisecond,
	}, logger, func(id string) { stoppedCh <- id })
	require.NoError(t, err)

	worker2, err := NewTestWorker("worker2", WorkerConfig{
		Interval: 150 * time.Millisecond,
	}, logger, func(id string) { stoppedCh <- id })
	require.NoError(t, err)

	// Initial configuration with two workers
	configEntries := []composite.RunnableEntry[*TestWorker]{
		{Runnable: worker1, Config: worker1.config},
		{Runnable: worker2, Config: worker2.config},
	}

	// Create simple config callback that always returns the current entries
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
		composite.WithContext[*TestWorker](ctx),
		composite.WithConfigCallback[*TestWorker](configCallback),
		composite.WithLogHandler[*TestWorker](logger.Handler()),
	)
	require.NoError(t, err)

	// Start runner in a goroutine
	doneCh := make(chan error, 1)
	go func() {
		doneCh <- runner.Run(ctx)
	}()

	// Wait for initial configuration to be applied
	time.Sleep(250 * time.Millisecond)

	// --- Test Step 1: Remove worker1, add worker3 ---
	worker3, err := NewTestWorker("worker3", WorkerConfig{
		Interval: 50 * time.Millisecond,
	}, logger, func(id string) { stoppedCh <- id })
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
	runner.Reload()

	// Allow time for reload to complete
	time.Sleep(300 * time.Millisecond)

	// Collect stopped workers - both worker1 and worker2 might be stopped
	// as the composite runner might stop and recreate worker2 during reload
	initialStopped := make(map[string]bool)

	// Wait for initial stops (allow up to 2 stops initially)
	stopCheckTimeout := time.After(500 * time.Millisecond)
	stopCount := 0

collectStops:
	for stopCount < 2 {
		select {
		case stopped := <-stoppedCh:
			t.Logf("Stopped worker: %s", stopped)
			initialStopped[stopped] = true
			stopCount++
		case <-stopCheckTimeout:
			break collectStops
		}
	}

	// Verify worker1 was stopped (this is what we're testing)
	require.True(t, initialStopped["worker1"], "Worker1 should have been stopped during reload")

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

	// Verify remaining workers were stopped
	stoppedCount := 0
	expectedStoppedWorkers := []string{"worker2", "worker3"}

	// Create a map to check which workers were stopped
	stoppedMap := make(map[string]bool)

drainLoop:
	for {
		select {
		case id := <-stoppedCh:
			stoppedMap[id] = true
			stoppedCount++
			t.Logf("Worker stopped during cleanup: %s", id)
		case <-time.After(300 * time.Millisecond):
			// No more workers stopped, break the loop
			break drainLoop
		}
	}

	// Verify the expected workers were stopped
	for _, id := range expectedStoppedWorkers {
		if !stoppedMap[id] {
			t.Errorf("Expected worker %s to be stopped, but it wasn't", id)
		}
	}

	// We should have seen 2 workers stopped during cleanup
	if stoppedCount < 2 {
		t.Errorf("Expected at least 2 workers to be stopped, got %d", stoppedCount)
	}

	t.Log("Test completed successfully")
}
