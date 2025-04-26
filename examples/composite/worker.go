package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robbyt/go-supervisor/runnables/composite"
	"github.com/robbyt/go-supervisor/supervisor"
)

// WorkerConfig is the configuration structure for the Worker, used for initial config and for
// dynamic updates via ReloadWithConfig at runtime.
type WorkerConfig struct {
	Interval time.Duration `json:"interval"`
	JobName  string        `json:"job_name"`
}

func (wc WorkerConfig) String() string {
	return fmt.Sprintf("WorkerConfig{JobName: %s, Interval: %s}", wc.JobName, wc.Interval)
}

func (wc WorkerConfig) Equal(other any) bool {
	otherConfig, ok := other.(WorkerConfig)
	if !ok {
		return false
	}
	return wc.Interval == otherConfig.Interval && wc.JobName == otherConfig.JobName
}

func (wc WorkerConfig) validate() error {
	if wc.Interval <= 0 {
		return fmt.Errorf("interval must be positive, got %v", wc.Interval)
	}
	if wc.JobName == "" {
		return errors.New("job name must not be empty")
	}
	return nil
}

// Worker is a simplified example of a runnable that does background work
type Worker struct {
	name       string
	mu         sync.Mutex
	config     WorkerConfig
	nextConfig chan WorkerConfig

	tickCount atomic.Int64
	tickChan  chan struct{}

	ctx          context.Context
	cancel       context.CancelFunc
	tickerCancel context.CancelFunc
	logger       *slog.Logger
}

// Ensure Worker implements these interfaces
var (
	_ supervisor.Runnable            = (*Worker)(nil)
	_ composite.ReloadableWithConfig = (*Worker)(nil)
)

// NewWorker creates a new Worker instance with the initial config.
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
	return fmt.Sprintf("Worker{config: %s}", w.getConfig())
}

// Run starts the worker's main loop.
func (w *Worker) Run(ctx context.Context) error {
	logger := w.logger.WithGroup("Run")
	w.mu.Lock()
	runCtx, cancel := context.WithCancel(ctx)
	w.ctx = runCtx
	w.cancel = cancel
	w.mu.Unlock()

	cfg := w.getConfig()
	logger = logger.With("name", cfg.JobName)
	logger.Info("Starting worker")

	w.startTicker(runCtx, cfg.Interval)
	defer func() {
		if tc := w.tickerCancel; tc != nil {
			tc()
		}
		logger.Info("Worker stopped")
	}()

	for {
		select {
		case <-runCtx.Done():
			logger.Debug("Worker context cancelled, shutting down")
			return nil
		case <-w.tickChan:
			w.processTick()
		case newConfig := <-w.nextConfig:
			w.processReload(&newConfig)
			cfg = w.getConfig()
			logger = logger.With("name", cfg.JobName)
		}
	}
}

// Stop signals the worker to gracefully shut down by cancelling its context.
func (w *Worker) Stop() {
	logger := w.logger.WithGroup("Stop")
	currentName := w.getConfig().JobName
	logger.Info("Stopping worker...", "name", currentName)
	if c := w.cancel; c != nil {
		w.cancel = nil
		c()
	}
	w.tickCount.Store(0)
}

// startTicker starts a new ticker goroutine that sends tick notifications to the worker.
func (w *Worker) startTicker(ctx context.Context, interval time.Duration) {
	logger := w.logger.WithGroup("startTicker")
	w.mu.Lock()
	oldTickerCancel := w.tickerCancel
	w.tickerCancel = nil
	w.mu.Unlock()
	if oldTickerCancel != nil {
		logger.Debug("Cancelling previous ticker goroutine")
		oldTickerCancel()
	}
	tickCtx, newTickerCancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.tickerCancel = newTickerCancel
	w.mu.Unlock()
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		defer logger.Debug("Ticker stopped")
		logger.Debug("Ticker started")
		for {
			select {
			case <-tickCtx.Done():
				logger.Debug("Ticker context cancelled, stopping ticker")
				return
			case <-ticker.C:
				select {
				case w.tickChan <- struct{}{}:
				default:
					logger.Warn("Tick notification dropped (tick channel full or closed)")
				}
			}
		}
	}()
	logger.Debug("Ticker setup complete", "interval", interval)
}

// ReloadWithConfig receives a new config from the composite Runnable, and sends it to the Run
// loop via a channel. It handles potential back-pressure by replacing a pending config with the
// latest received if the channel is full.
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
	logger.Debug("ReloadWithConfig called", "cfg", w.getConfig())

	// Validate the received config immediately
	if err := cfg.validate(); err != nil {
		logger.Error(
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

// getConfig returns the worker's current configuration safely.
func (w *Worker) getConfig() WorkerConfig {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.config
}

// setConfig updates the internal configuration state safely.
// It returns the *previous* config object.
func (w *Worker) setConfig(newConfig WorkerConfig) (oldCfg WorkerConfig) {
	w.mu.Lock()
	defer w.mu.Unlock()
	oldCfg = w.config
	w.config = newConfig
	if w.name != newConfig.JobName {
		w.name = newConfig.JobName
	}
	w.logger.Debug("Updated config", "old", oldCfg, "new", newConfig)
	return oldCfg
}

// processReload is a helper function to process the new configuration.
func (w *Worker) processReload(newConfig *WorkerConfig) {
	logger := w.logger.WithGroup("processReload").With("newConfig", newConfig)
	currentCfg := w.getConfig()
	if err := newConfig.validate(); err != nil {
		logger.Error(
			"Received invalid configuration, discarding",
			"name", currentCfg.JobName,
			"newCfg", newConfig,
			"error", err,
		)
		return
	}
	oldCfg := w.setConfig(*newConfig)
	if oldCfg.Interval == newConfig.Interval {
		logger.Debug(
			"Interval unchanged, skipping ticker restart",
			"name", newConfig.JobName,
		)
		return
	}
	logger.Info(
		"Interval changed, resetting ticker",
		"name", newConfig.JobName,
		"oldInterval", oldCfg.Interval,
		"newInterval", newConfig.Interval,
	)
	w.mu.Lock()
	runCtx := w.ctx
	w.mu.Unlock()
	if runCtx == nil {
		logger.Error(
			"Cannot restart ticker, run context is nil",
			"name", newConfig.JobName,
		)
		return
	}
	w.startTicker(runCtx, newConfig.Interval)
	w.tickCount.Store(0)
}

// processTick performs the actual work on each tick.
func (w *Worker) processTick() {
	cfg := w.getConfig()
	time.Sleep(1 * time.Millisecond)
	count := w.tickCount.Add(1)
	w.logger.Info(
		"Performing work",
		"interval", cfg.Interval,
		"name", cfg.JobName,
		"tick_count", count,
	)
	// --- Add your actual worker logic here ---
}
