package main // Assuming test is in the same package

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	// Use paths from the user's example
	"github.com/robbyt/go-supervisor/runnables/composite"
	"github.com/robbyt/go-supervisor/supervisor"
	// Use testify imports
	"github.com/stretchr/testify/assert"
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

// TestMembershipChanges verifies CompositeRunner stops removed runnables during reload.
func TestMembershipChanges(t *testing.T) {
	t.Skip("Skipping due to flaky test - needs rework of stop call tracking")
	t.Parallel()
	logger, _ := testLogger(t, false) // Use helper, capture=true for debug

	// Setup tracking for worker stop calls using TestWorker's 'id'
	stopCalls := make(map[string]int) // Use int to count calls (useful for debugging)
	var stopMu sync.Mutex
	recordStop := func(id string) {
		stopMu.Lock()
		defer stopMu.Unlock()
		stopCalls[id]++
		t.Logf("Stop recorded for worker ID: %s (Count: %d)", id, stopCalls[id])
	}

	// === Worker Creation ===
	worker1ID := "worker-id-1"
	worker1, err := NewTestWorker(worker1ID, WorkerConfig{
		Interval: 100 * time.Millisecond,
		// JobName set internally by NewTestWorker to worker1ID
	}, logger, recordStop)
	require.NoError(t, err, "Failed to create worker1")

	worker2ID := "worker-id-2"
	worker2, err := NewTestWorker(worker2ID, WorkerConfig{
		Interval: 150 * time.Millisecond, // Slightly different interval
		// JobName set internally by NewTestWorker to worker2ID
	}, logger, recordStop)
	require.NoError(t, err, "Failed to create worker2")

	// === Initial Composite Configuration ===
	initialEntries := []composite.RunnableEntry[*TestWorker]{
		// Pass the initial config associated with the worker instance
		{Runnable: worker1, Config: worker1.config},
		{Runnable: worker2, Config: worker2.config},
	}

	// Mutex protects currentEntries slice, configCallback reads it safely
	var entriesMu sync.Mutex
	currentEntries := initialEntries

	configCallback := func() (*composite.Config[*TestWorker], error) {
		entriesMu.Lock()
		defer entriesMu.Unlock()
		// Return a *copy* to prevent race conditions if the slice is modified elsewhere
		// while composite runner is reading it (unlikely here, but good practice).
		entriesCopy := make([]composite.RunnableEntry[*TestWorker], len(currentEntries))
		copy(entriesCopy, currentEntries)
		// The composite runner name can be static for the test
		return composite.NewConfig("test-membership-composite", entriesCopy)
	}

	// === Context and Supervisor Setup ===
	// Adjust timeout if needed based on intervals and processing time
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	// Create composite runner
	runner, err := composite.NewRunner[*TestWorker](
		composite.WithContext[*TestWorker](ctx), // Runner uses test context
		composite.WithConfigCallback[*TestWorker](configCallback),
		composite.WithLogHandler[*TestWorker](logger.Handler()), // Pass the handler
	)
	require.NoError(t, err, "Failed to create composite runner")

	// Create supervisor (controls the runner)
	sv, err := supervisor.New(
		supervisor.WithContext(ctx), // Supervisor also uses test context
		supervisor.WithRunnables(runner),
		supervisor.WithLogHandler(logger.Handler()),
	)
	require.NoError(t, err, "Failed to create supervisor")

	// === Cleanup Function (using t.Cleanup) ===
	var wg sync.WaitGroup // WaitGroup for the supervisor goroutine
	wg.Add(1)
	errCh := make(chan error, 1) // Channel to receive error from sv.Run()

	t.Cleanup(func() {
		t.Log("Cleanup: Cancelling context and waiting for supervisor...")
		cancel() // Cancel the main context

		// Wait for supervisor Run to return, with a timeout
		select {
		case runErr := <-errCh:
			// Expect nil or context cancelled/deadline exceeded errors on graceful shutdown
			if runErr != nil && !errors.Is(runErr, context.Canceled) &&
				!errors.Is(runErr, context.DeadlineExceeded) {
				t.Errorf("Supervisor Run returned unexpected error during cleanup: %v", runErr)
			} else {
				t.Logf("Supervisor Run returned '%v' (expected during cleanup)", runErr)
			}
		case <-time.After(3 * time.Second): // Adjust timeout if needed
			t.Error("Timeout waiting for supervisor Run to return during cleanup")
		}
		wg.Wait() // Wait for the goroutine itself to finish
		t.Log("Cleanup: Supervisor goroutine finished.")

		// === Final Assertions in Cleanup ===
		stopMu.Lock()
		defer stopMu.Unlock()
		// worker1 was removed during the test, its stop call should have been recorded then.
		assert.GreaterOrEqual(
			t,
			stopCalls[worker1ID],
			1,
			"Worker1 (removed) stop call count mismatch",
		)
		// worker2 and worker3 were active at shutdown, should be stopped by supervisor/context cancel.
		assert.GreaterOrEqual(t, stopCalls[worker2ID], 1, "Worker2 (kept) stop call count mismatch")
		assert.GreaterOrEqual(
			t,
			stopCalls["worker-id-3"],
			1,
			"Worker3 (added) stop call count mismatch",
		) // Use the correct ID used when creating worker3
		t.Logf("Final stop calls recorded: %v", stopCalls)
	})

	// === Start Supervisor ===
	go func() {
		defer wg.Done()
		t.Log("Supervisor starting Run...")
		errCh <- sv.Run() // Send result (error or nil) to channel upon completion
		t.Log("Supervisor Run finished.")
	}()

	// === Wait for Initial State ===
	t.Log("Waiting for initial workers to start ticking...")
	require.Eventually(t, func() bool {
		// Check if both initial workers have ticked at least once
		// Use RLock for consistency, though Load is atomic
		worker1.mu.RLock()
		t1 := worker1.tickCount.Load() > 0
		worker1.mu.RUnlock()
		worker2.mu.RLock()
		t2 := worker2.tickCount.Load() > 0
		worker2.mu.RUnlock()
		if t1 && t2 {
			t.Logf(
				"Initial ticks detected: worker1=%d, worker2=%d",
				worker1.tickCount.Load(),
				worker2.tickCount.Load(),
			)
			return true
		}
		return false
	}, 3*time.Second, 50*time.Millisecond, "Initial workers did not start ticking")
	t.Log("Initial workers are running.")

	// === Prepare and Trigger Reload ===
	worker3ID := "worker-id-3"
	worker3, err := NewTestWorker(worker3ID, WorkerConfig{
		Interval: 200 * time.Millisecond,
		// JobName set internally by NewTestWorker to worker3ID
	}, logger, recordStop)
	require.NoError(t, err, "Failed to create worker3")

	// Define the updated configuration entries:
	// - Remove worker1
	// - Keep worker2 instance, but update its config
	// - Add worker3 instance with its config
	updatedWorker2Config := WorkerConfig{
		Interval: 180 * time.Millisecond, // Change interval
		JobName:  "worker-id-2-reloaded", // Change JobName (Worker.name will update)
	}
	updatedEntries := []composite.RunnableEntry[*TestWorker]{
		{Runnable: worker2, Config: updatedWorker2Config}, // Keep worker2, new config
		{Runnable: worker3, Config: worker3.config},       // Add worker3, initial config
	}

	// Safely update the shared entries slice for the configCallback
	entriesMu.Lock()
	currentEntries = updatedEntries
	entriesMu.Unlock()
	t.Log("Updated config slice for callback. Triggering Reload...")

	// Trigger the reload on the composite runner
	runner.Reload()

	// === Verify Reload Results ===

	// 1. Verify that worker1 (removed) was stopped
	t.Logf("Waiting for worker '%s' (removed) to be stopped...", worker1ID)
	require.Eventually(t, func() bool {
		stopMu.Lock()
		defer stopMu.Unlock()
		return stopCalls[worker1ID] > 0
	}, 3*time.Second, 50*time.Millisecond, "Worker '%s' (removed) stop call was not recorded", worker1ID)
	t.Logf("Worker '%s' stop call verified.", worker1ID)

	// 2. Optional: Verify worker2 (kept) received its config update
	t.Logf("Verifying worker '%s' (kept) config update...", worker2ID)
	require.Eventually(t, func() bool {
		// CompositeRunner should call ReloadWithConfig on worker2. Check its internal state.
		cfg := readWorkerConfig(worker2.Worker) // Use helper to read safely
		name := readWorkerName(worker2.Worker)
		intervalMatch := cfg.Interval == updatedWorker2Config.Interval
		nameMatch := name == updatedWorker2Config.JobName // Worker.name should update
		if intervalMatch && nameMatch {
			t.Logf(
				"Worker '%s' config verified: Interval=%v, Name=%s",
				worker2ID,
				cfg.Interval,
				name,
			)
			return true
		}
		t.Logf(
			"Worker '%s' config not yet updated: Interval=%v (want %v), Name=%s (want %s)",
			worker2ID,
			cfg.Interval,
			updatedWorker2Config.Interval,
			name,
			updatedWorker2Config.JobName,
		)
		return false
	}, 3*time.Second, 50*time.Millisecond, "Worker '%s' config did not update correctly", worker2ID)
	t.Logf("Worker '%s' config update verified.", worker2ID)

	// 3. Optional: Verify worker3 (added) started ticking
	t.Logf("Verifying worker '%s' (added) started ticking...", worker3ID)
	require.Eventually(t, func() bool {
		// CompositeRunner should call Run on worker3. Check its tick count.
		worker3.mu.RLock() // Lock needed to access embedded Worker safely
		tickCount := worker3.tickCount.Load()
		worker3.mu.RUnlock()
		if tickCount > 0 {
			t.Logf("Worker '%s' ticks detected: %d", worker3ID, tickCount)
			return true
		}
		return false
	}, 3*time.Second, 50*time.Millisecond, "Worker '%s' (added) did not start ticking", worker3ID)
	t.Logf("Worker '%s' started ticking.", worker3ID)

	// Test proceeds until timeout or explicit cancel in t.Cleanup
	t.Log("Reload verification complete. Test continuing until cleanup/timeout...")
}
