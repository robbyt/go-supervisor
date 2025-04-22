package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	// Assuming this path is correct for your project setup
	"github.com/robbyt/go-supervisor/runnables/composite"
)

// WorkerConfig holds configuration for a worker runnable
type WorkerConfig struct {
	Interval time.Duration `json:"interval"`
	JobName  string        `json:"job_name"`
}

func (wc WorkerConfig) String() string {
	return fmt.Sprintf("WorkerConfig{JobName: %s, Interval: %s}", wc.JobName, wc.Interval)
}

// validate checks if a WorkerConfig is valid.
func (wc WorkerConfig) validate() error {
	if wc.Interval <= 0 {
		return fmt.Errorf("interval must be positive, got %v", wc.Interval)
	}
	if wc.JobName == "" {
		return errors.New("job name must not be empty")
	}
	return nil
}

func (wc WorkerConfig) Equal(other any) bool {
	otherConfig, ok := other.(WorkerConfig)
	if !ok {
		return false
	}
	return wc.Interval == otherConfig.Interval && wc.JobName == otherConfig.JobName
}

// Worker is a simplified example of a runnable that does background work
type Worker struct {
	name       string
	mu         sync.RWMutex      // Use RWMutex for better read performance
	config     WorkerConfig      // Current running config
	nextConfig chan WorkerConfig // Channel for pending config updates from ReloadWithConfig

	tickCount atomic.Int64  // Counter for processed ticks
	tickChan  chan struct{} // Channel for tick notifications

	ctx          context.Context
	cancel       context.CancelFunc // Function to cancel the Run context
	tickerCancel context.CancelFunc // Function to cancel the ticker goroutine
	logger       *slog.Logger
}

// Ensure Worker implements the composite.ReloadableWithConfig interface
var _ composite.ReloadableWithConfig = (*Worker)(nil)

// NewWorker creates a new Worker with the given config
// Returns an error if the initial config is invalid.
func NewWorker(config WorkerConfig, logger *slog.Logger) (*Worker, error) {
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("invalid initial configuration: %w", err)
	}
	if logger == nil {
		logger = slog.Default().WithGroup(fmt.Sprintf("worker.%s", config.JobName))
	}

	return &Worker{
		name:       config.JobName,
		config:     config,
		nextConfig: make(chan WorkerConfig, 1), // Buffer of 1 allows one pending config
		tickChan:   make(chan struct{}, 1),     // Buffer of 1 allows one tick to be pending
		logger:     logger,
	}, nil
}

// String returns the worker's name and interval safely.
func (w *Worker) String() string {
	return fmt.Sprintf("Worker{config: %s}", w.GetConfig())
}

// Run starts the worker's main loop.
func (w *Worker) Run(ctx context.Context) error {
	logger := w.logger.WithGroup("Run")
	w.mu.Lock()
	runCtx, cancel := context.WithCancel(ctx)
	w.ctx = runCtx
	w.cancel = cancel // Store cancel function for Stop()
	w.mu.Unlock()

	cfg := w.GetConfig()
	logger.Info("Starting worker", "cfg", cfg.JobName)

	w.startTicker(runCtx, cfg.Interval)
	defer func() {
		if w.tickerCancel != nil {
			w.tickerCancel() // Stop the ticker goroutine
		}
		logger.Info("Worker stopped", "name", w.GetConfig().JobName)
	}()

	for {
		select {
		case <-runCtx.Done():
			logger.Debug("Worker context cancelled, shutting down", "name", w.GetConfig().JobName)
			return nil // Normal exit

		case <-w.tickChan:
			w.processTick()

		case newConfig := <-w.nextConfig:
			w.processReload(&newConfig)
		}
	}
}

// Stop signals the worker to gracefully shut down by cancelling its context.
func (w *Worker) Stop() {
	w.logger.WithGroup("Stop").Info("Stopping worker...", "name", w.GetConfig().JobName)
	if w.cancel != nil {
		w.cancel() // Signal the Run loop to exit via context cancellation
	}
	w.tickCount.Store(0)
}

// startTicker starts a new ticker goroutine that sends tick notifications to the worker.
func (w *Worker) startTicker(ctx context.Context, interval time.Duration) {
	tickCtx, tickCancel := context.WithCancel(ctx)
	w.tickerCancel = tickCancel
	logger := w.logger.WithGroup("startTicker")
	ticker := time.NewTicker(interval)

	// Start a goroutine to push ticks to the worker's tick channel
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-tickCtx.Done():
				// Ticker goroutine context was cancelled
				logger.Debug("Ticker context cancelled, stopping ticker")
				return
			case <-ticker.C:
				select {
				case w.tickChan <- struct{}{}:
					// tick sent successfully
				default:
					// Tick channel is full, log a warning
					logger.Warn("Tick notification dropped (tick channel full)")
				}
			}
		}
	}()
	logger.Debug("Ticker started", "interval", interval)
}

// Reload is part of the Reloadable interface. In this pattern, config updates
// are primarily handled via ReloadWithConfig and processed asynchronously by the Run loop.
// This Reload implementation will simply log the action. If a config is pending
// in nextConfig, the Run loop will pick it up anyway. Draining it here could be problematic.
func (w *Worker) Reload() {
	w.logger.WithGroup("Reload").Info("Reload called, but no action taken")
	// No action taken here; ReloadWithConfig handles the actual config update.
	// This is a placeholder to satisfy the Reloadable interface.
}

// ReloadWithConfig receives a new configuration and sends it to the Run loop via a channel.
// It handles potential backpressure by replacing the pending config if the channel is full.
func (w *Worker) ReloadWithConfig(config any) {
	logger := w.logger.WithGroup("ReloadWithConfig")
	cfg, ok := config.(WorkerConfig)
	if !ok {
		w.logger.Error(
			"Invalid config type received for ReloadWithConfig",
			"type", fmt.Sprintf("%T", config),
		)
		return
	}
	logger.Debug("ReloadWithConfig called", "cfg", w.GetConfig())

	// Validate the received config immediately
	if err := cfg.validate(); err != nil {
		w.logger.Error(
			"Invalid configuration received in ReloadWithConfig, discarding",
			"error", err,
			"config", cfg,
		)
		return
	}

	// Non-blocking send to nextConfig channel.
	// If the channel is full (meaning a config is already waiting),
	// discard the waiting one and queue the new one (newer config wins).
	select {
	case w.nextConfig <- cfg:
		logger.Info(
			"Configuration queued for update",
			"cfg", cfg)
	default:
		// Channel is full, discard the old pending config and add the new one.
		old := <-w.nextConfig

		logger.Info(
			"Discarding old config",
			"old", old,
			"new", cfg)

		w.nextConfig <- cfg

		logger.Info(
			"Replaced pending configuration with the newest one",
			"new", cfg)
	}
}

// GetConfig returns the worker's current configuration safely.
func (w *Worker) GetConfig() WorkerConfig {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.config
}

// setConfig updates the internal configuration state safely.
// It returns the *previous* config object.
func (w *Worker) setConfig(newConfig WorkerConfig) (oldCfg WorkerConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()

	oldCfg = w.config
	w.config = newConfig
	w.name = newConfig.JobName

	w.logger.Debug("Updated config",
		"old", oldCfg, "new", newConfig)
	return oldCfg
}

// processReload is a helper function to process the new configuration.
func (w *Worker) processReload(newConfig *WorkerConfig) {
	logger := w.logger.WithGroup("handleReload").With("newConfig", newConfig)
	currentCfg := w.GetConfig()
	if err := newConfig.validate(); err != nil {
		logger.Error(
			"Received invalid configuration, discarding",
			"name", currentCfg.JobName,
			"newCfg", newConfig,
			"error", err,
		)
		return
	}

	// Apply the new configuration and get the *previous* config
	oldCfg := w.setConfig(*newConfig)
	if oldCfg.Interval == newConfig.Interval {
		logger.Debug("Interval unchanged, skipping update", "name", currentCfg.JobName)
		return
	}

	logger.Info(
		"Interval changed, resetting ticker",
		"old", oldCfg,
		"new", newConfig,
	)
	if w.tickerCancel != nil {
		w.tickerCancel()
	}
	w.startTicker(w.ctx, newConfig.Interval)
	w.tickCount.Store(0) // Reset tick count on ticker reset reload
}

// processTick performs the actual work on each tick.
func (w *Worker) processTick() {
	w.mu.RLock()
	// Read necessary config under read lock
	cfg := w.config
	currentName := w.name // Read name under lock too
	w.mu.RUnlock()

	// Simulate work - Replace with your actual task
	time.Sleep(1 * time.Millisecond)
	count := w.tickCount.Add(1)

	w.logger.Info(
		"Performing work",
		"interval", cfg.Interval,
		"name", currentName,
		"tick_count", count,
	)
	// --- Add your actual worker logic here ---
}
