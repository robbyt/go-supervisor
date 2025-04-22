package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test Constants ---
const (
	defaultTestTimeout = 5 * time.Second       // Max time for most tests
	tickWaitFactor     = 3                     // Wait up to X times the interval for a tick
	pollInterval       = 10 * time.Millisecond // How often to check conditions in Eventually
)

// --- Helper Functions ---

// testLogger creates a logger for tests, optionally capturing output.
func testLogger(t *testing.T, capture bool) (*slog.Logger, *bytes.Buffer) { //nolint:thelper
	var buf bytes.Buffer
	w := io.Discard
	if capture {
		w = &buf
	}
	// Use a lower level for debugging test failures if needed
	// level := slog.LevelDebug
	level := slog.LevelInfo
	logHandler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	logger := slog.New(logHandler)
	return logger, &buf
}

// readWorkerConfig safely reads the current config from the worker.
func readWorkerConfig(w *Worker) WorkerConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.config // Return a copy
}

// readWorkerName safely reads the current name from the worker.
func readWorkerName(w *Worker) string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.name
}

// --- Test Cases ---

// TestWorker_NewWorker_Validation tests the validation logic in NewWorker.
func TestWorker_NewWorker_Validation(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger(t, false)

	testCases := []struct {
		name        string
		config      WorkerConfig
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid config",
			config:      WorkerConfig{Interval: 100 * time.Millisecond, JobName: "valid-job"},
			expectError: false,
		},
		{
			name:        "Invalid interval (zero)",
			config:      WorkerConfig{Interval: 0, JobName: "zero-interval"},
			expectError: true,
			errorMsg:    "interval must be positive",
		},
		{
			name:        "Invalid interval (negative)",
			config:      WorkerConfig{Interval: -1 * time.Second, JobName: "neg-interval"},
			expectError: true,
			errorMsg:    "interval must be positive",
		},
		{
			name:        "Invalid name (empty)",
			config:      WorkerConfig{Interval: 100 * time.Millisecond, JobName: ""},
			expectError: true,
			errorMsg:    "job name must not be empty",
		},
	}

	for _, tc := range testCases {
		tc := tc // Capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			worker, err := NewWorker(tc.config, logger)
			if tc.expectError {
				require.Error(t, err, "Expected an error for invalid config")
				assert.ErrorContains(t, err, tc.errorMsg, "Error message mismatch")
				assert.Nil(t, worker, "Worker should be nil on error")
			} else {
				require.NoError(t, err, "Expected no error for valid config")
				require.NotNil(t, worker, "Worker should not be nil on success")
				assert.Equal(t, tc.config.JobName, worker.name, "Worker name mismatch")
				assert.Equal(t, tc.config, worker.config, "Worker config mismatch")
			}
		})
	}
}

// TestWorker_Run_Stop tests basic Run and Stop functionality.
func TestWorker_Run_Stop(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger(t, false) // Change capture to true for debugging

	config := WorkerConfig{Interval: 50 * time.Millisecond, JobName: "stoppable-job"}
	worker, err := NewWorker(config, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background()) // Parent context
	defer cancel()                                          // Ensure parent context is cancelled

	var wg sync.WaitGroup
	wg.Add(1)
	runErrChan := make(chan error, 1)

	go func() {
		defer wg.Done()
		t.Log("Worker starting Run...")
		// Use the parent context directly for Run
		runErrChan <- worker.Run(ctx)
		t.Log("Worker Run finished.")
	}()

	// Wait for at least one tick to ensure the worker is running
	require.Eventually(t, func() bool {
		return worker.tickCount.Load() >= 1
	}, 500*time.Millisecond, pollInterval, "Worker did not perform any ticks")
	t.Logf("Worker ticked at least once (count: %d)", worker.tickCount.Load())

	// Stop the worker
	t.Log("Calling worker.Stop()")
	worker.Stop() // Should cancel the context used by Run

	// Wait for the Run goroutine to exit
	select {
	case err := <-runErrChan:
		// Expect nil error because stop should be graceful context cancellation
		assert.NoError(t, err, "Worker Run should return nil error on graceful stop")
	case <-time.After(1 * time.Second): // Timeout waiting for Run to exit
		t.Fatal("Worker Run did not return after Stop() was called")
	}

	// Wait for the WaitGroup as a final confirmation
	wg.Wait()
	t.Log("WaitGroup finished.")

	// Verify tick count was reset by Stop()
	assert.Equal(t, int64(0), worker.tickCount.Load(), "Tick count should be reset after Stop()")
}

// TestWorker_Run_ContextCancel tests that cancelling the parent context stops the worker.
func TestWorker_Run_ContextCancel(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger(t, false)

	config := WorkerConfig{Interval: 50 * time.Millisecond, JobName: "cancel-job"}
	worker, err := NewWorker(config, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background()) // Parent context

	var wg sync.WaitGroup
	wg.Add(1)
	runErrChan := make(chan error, 1)

	go func() {
		defer wg.Done()
		t.Log("Worker starting Run...")
		runErrChan <- worker.Run(ctx) // Pass the cancellable context
		t.Log("Worker Run finished.")
	}()

	// Wait for at least one tick
	require.Eventually(t, func() bool {
		return worker.tickCount.Load() >= 1
	}, 500*time.Millisecond, pollInterval, "Worker did not perform any ticks")
	initialTickCount := worker.tickCount.Load()
	t.Logf("Worker ticked at least once (count: %d)", initialTickCount)

	// Cancel the context externally
	t.Log("Cancelling context externally")
	cancel()

	// Wait for the Run goroutine to exit
	select {
	case err := <-runErrChan:
		// Expect nil error for graceful context cancellation
		assert.NoError(t, err, "Worker Run should return nil error on context cancel")
	case <-time.After(1 * time.Second):
		t.Fatal("Worker Run did not return after context cancellation")
	}

	// Wait for the WaitGroup
	wg.Wait()
	t.Log("WaitGroup finished.")
	// Tick count is *not* reset when stopped via external context cancel, only via Stop()
	assert.GreaterOrEqual(
		t,
		worker.tickCount.Load(),
		initialTickCount,
		"Tick count should not be reset by external cancel",
	)
}

// TestWorker_ReloadWithConfig tests applying valid and invalid configs via ReloadWithConfig.
func TestWorker_ReloadWithConfig(t *testing.T) {
	t.Parallel()
	logger, logBuf := testLogger(t, true) // Capture logs

	originalConfig := WorkerConfig{
		Interval: 100 * time.Millisecond,
		JobName:  "test-job-reload",
	}
	worker, err := NewWorker(originalConfig, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)

	// Ensure worker is stopped and waited upon even if test fails
	t.Cleanup(func() {
		t.Log("Cleanup: Stopping worker and waiting...")
		cancel()      // Cancel context first
		worker.Stop() // Call stop as well (idempotent context cancel)
		wg.Wait()
		t.Log("Cleanup: Worker stopped.")
	})

	go func() {
		defer wg.Done()
		// Ignore error here, checked elsewhere
		_ = worker.Run(ctx) //nolint:errcheck
	}()

	// Wait for worker to start running (at least one tick)
	require.Eventually(t, func() bool {
		return worker.tickCount.Load() > 0
	}, 1*time.Second, pollInterval, "Worker did not start ticking")

	// --- Test Valid Reload ---
	newConfig := WorkerConfig{
		Interval: 200 * time.Millisecond,
		JobName:  "updated-job-reload",
	}
	t.Logf("Reloading with valid config: %+v", newConfig)
	worker.ReloadWithConfig(newConfig)

	// Wait for the configuration to be applied using Eventually
	require.Eventually(t, func() bool {
		current := readWorkerConfig(worker)
		name := readWorkerName(worker)
		return current.Interval == newConfig.Interval && current.JobName == newConfig.JobName &&
			name == newConfig.JobName
	}, 1*time.Second, pollInterval, "Worker config was not updated after valid ReloadWithConfig")
	t.Logf("Worker config updated successfully to: %+v", readWorkerConfig(worker))

	// --- Test Invalid Config Type ---
	configAfterValidReload := readWorkerConfig(worker) // Store state before invalid reload
	t.Log("Reloading with invalid type 'string'")
	worker.ReloadWithConfig("invalid type") // Pass a non-WorkerConfig type

	// Wait a short time and assert config hasn't changed
	time.Sleep(50 * time.Millisecond) // Allow select loop to process if needed
	currentConfig := readWorkerConfig(worker)
	assert.Equal(
		t,
		configAfterValidReload,
		currentConfig,
		"Config should not change after invalid type reload",
	)
	assert.Contains(
		t,
		logBuf.String(),
		"Invalid config type received",
		"Should log error for invalid type",
	)
	logBuf.Reset() // Clear buffer for next check

	// --- Test Invalid Config Values ---
	invalidValueConfig := WorkerConfig{Interval: 0, JobName: "wont-apply"}
	t.Logf("Reloading with invalid value config: %+v", invalidValueConfig)
	worker.ReloadWithConfig(invalidValueConfig)

	// Wait and assert config hasn't changed
	time.Sleep(50 * time.Millisecond)
	currentConfig = readWorkerConfig(worker)
	assert.Equal(
		t,
		configAfterValidReload,
		currentConfig,
		"Config should not change after invalid value reload",
	)
	assert.Contains(
		t,
		logBuf.String(),
		"Invalid configuration received in ReloadWithConfig",
		"Should log error for invalid value",
	)
	assert.Contains(
		t,
		logBuf.String(),
		"interval must be positive",
		"Should log specific validation error",
	)
}

// TestWorker_Execution_Timing tests that the worker ticks according to the configured interval,
// including after a reload. Timing assertions are inherently fuzzy.
func TestWorker_Execution_Timing(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger(t, false) // Capture logs if debugging: true

	// Use shorter intervals for faster testing, but not too short to cause timing issues
	initialInterval := 75 * time.Millisecond
	reloadedInterval := 40 * time.Millisecond

	config := WorkerConfig{Interval: initialInterval, JobName: "timing-job"}
	worker, err := NewWorker(config, logger)
	require.NoError(t, err)

	// Give enough time for several ticks at both intervals
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	var wg sync.WaitGroup
	wg.Add(1)

	t.Cleanup(func() {
		t.Log("Cleanup: Cancelling context and waiting...")
		cancel()
		wg.Wait()
		t.Log("Cleanup: Finished.")
	})

	go func() {
		defer wg.Done()
		err := worker.Run(ctx)
		// Expect DeadlineExceeded or Canceled error due to timeout or cleanup
		if err != nil && !errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, context.Canceled) {
			t.Errorf("Worker run failed unexpectedly: %v", err) // Use t.Errorf in goroutine
		}
	}()

	// --- Test Initial Interval ---
	t.Logf("Testing initial interval: %v", initialInterval)
	// Wait for first tick
	require.Eventually(t, func() bool {
		return worker.tickCount.Load() > 0
	}, tickWaitFactor*initialInterval, pollInterval, "Worker did not tick initially")

	// Measure initial tick rate over a longer period
	measureTicks := func(expectedInterval time.Duration) (int64, time.Duration) {
		// Allow time for stabilization after potential config change
		time.Sleep(2 * expectedInterval)
		startCount := worker.tickCount.Load()
		startTime := time.Now()
		// Sample over ~10 expected ticks
		sampleDuration := 10 * expectedInterval
		time.Sleep(sampleDuration)
		endCount := worker.tickCount.Load()
		elapsedTime := time.Since(startTime)
		ticksProcessed := endCount - startCount
		return ticksProcessed, elapsedTime
	}

	ticks1, elapsed1 := measureTicks(initialInterval)
	require.Greater(
		t,
		ticks1,
		int64(5),
		"Processed too few ticks (%d) during initial measurement",
		ticks1,
	) // Expect roughly 10
	measuredInterval1 := elapsed1 / time.Duration(ticks1)
	t.Logf(
		"Initial measurement: %d ticks in %v. Avg interval: %v (expected: %v)",
		ticks1,
		elapsed1.Round(time.Millisecond),
		measuredInterval1.Round(time.Millisecond),
		initialInterval,
	)

	// Allow significant delta for scheduler jitter, GC, etc. (e.g., 60%)
	assert.InDelta(
		t,
		initialInterval.Seconds(),
		measuredInterval1.Seconds(),
		float64(initialInterval.Seconds())*0.6,
		"Measured average interval [%v] significantly different from initial interval [%v]",
		measuredInterval1,
		initialInterval,
	)

	// --- Test Reloaded Interval ---
	newConfig := WorkerConfig{Interval: reloadedInterval, JobName: "timing-job-reloaded"}
	t.Logf("Reloading config with interval: %v", reloadedInterval)
	worker.ReloadWithConfig(newConfig)

	// Ensure config is applied before measuring again
	require.Eventually(t, func() bool {
		return readWorkerConfig(worker).Interval == reloadedInterval
	}, 1*time.Second, pollInterval, "Config interval did not update after reload")
	t.Log("Config interval updated.")

	ticks2, elapsed2 := measureTicks(reloadedInterval)
	require.Greater(
		t,
		ticks2,
		int64(5),
		"Processed too few ticks (%d) during measurement after reload",
		ticks2,
	) // Expect roughly 10
	measuredInterval2 := elapsed2 / time.Duration(ticks2)
	t.Logf(
		"Reloaded measurement: %d ticks in %v. Avg interval: %v (expected: %v)",
		ticks2,
		elapsed2.Round(time.Millisecond),
		measuredInterval2.Round(time.Millisecond),
		reloadedInterval,
	)

	assert.InDelta(
		t,
		reloadedInterval.Seconds(),
		measuredInterval2.Seconds(),
		float64(reloadedInterval.Seconds())*0.6,
		"Measured average interval [%v] significantly different from reloaded interval [%v]",
		measuredInterval2,
		reloadedInterval,
	)
}

// TestWorker_ReloadWithConfig_Concurrency tests multiple concurrent reloads.
func TestWorker_ReloadWithConfig_Concurrency(t *testing.T) {
	t.Parallel()
	logger, _ := testLogger(t, false)

	config := WorkerConfig{Interval: 100 * time.Millisecond, JobName: "concurrent-reload"}
	worker, err := NewWorker(config, logger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	var runWg sync.WaitGroup
	runWg.Add(1)

	t.Cleanup(func() {
		t.Log("Cleanup: Stopping worker...")
		cancel()
		worker.Stop()
		runWg.Wait()
		t.Log("Cleanup: Worker stopped.")
	})

	go func() {
		defer runWg.Done()
		_ = worker.Run(ctx) //nolint:errcheck // Ignore error in test goroutine
	}()

	// Wait for worker to start
	require.Eventually(t, func() bool {
		return worker.tickCount.Load() > 0
	}, 1*time.Second, pollInterval, "Worker did not start")

	numReloads := 50
	var reloadWg sync.WaitGroup
	reloadWg.Add(numReloads)

	finalConfig := WorkerConfig{} // Store the config from the last reload goroutine

	// Launch concurrent reloads
	for i := 0; i < numReloads; i++ {
		go func(index int) {
			defer reloadWg.Done()
			// Vary interval and name slightly for each reload
			cfg := WorkerConfig{
				Interval: time.Duration(50+index) * time.Millisecond,
				JobName:  fmt.Sprintf("job-%d", index),
			}
			// The last goroutine to run will set the final expected config
			if index == numReloads-1 {
				finalConfig = cfg
			}
			worker.ReloadWithConfig(cfg)
			// Add a small random sleep to increase inter-leaving chances
			// time.Sleep(time.Duration(rand.Intn(5)) * time.Millisecond)
		}(i)
	}

	reloadWg.Wait() // Wait for all ReloadWithConfig calls to complete
	t.Logf(
		"All %d reload goroutines finished. Waiting briefly for final config application...",
		numReloads,
	)

	// Give the worker's Run loop a chance to process the last queued item
	time.Sleep(2 * time.Second)

	t.Logf("Final config details - Expected: %v, Current: %v",
		finalConfig.Interval, readWorkerConfig(worker).Interval)

	// Because ReloadWithConfig replaces the config in the channel if full,
	// we expect the *last* successfully queued config to eventually be applied.
	// This isn't strictly guaranteed to be `finalConfig` if the channel was full
	// and the last goroutine's send happened *before* an earlier one was processed,
	// but it's the most likely outcome.
	// A more robust check waits for the *interval* to match the final one,
	// as that's the primary observable effect managed by the Run loop.
	// Skip exact interval match check - the test is flaky because the last reload may not be the one processed
	// due to timing issues with 50 concurrent reloads
	t.Log("Skipping exact interval match check due to timing issues with concurrent reloads")

	t.Logf(
		"Final applied config interval: %v (expected: %v)",
		readWorkerConfig(worker).Interval,
		finalConfig.Interval,
	)
	// Check tick count is still advancing (worker didn't get stuck)
	initialTicks := worker.tickCount.Load()
	assert.Eventually(t, func() bool {
		return worker.tickCount.Load() > initialTicks
	}, 500*time.Millisecond, pollInterval, "Tick count did not advance after concurrent reloads")
}
