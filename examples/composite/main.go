package main

import (
	"context"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/robbyt/go-supervisor/runnables/composite"
	"github.com/robbyt/go-supervisor/supervisor"
)

func randInterval() time.Duration {
	return time.Duration(rand.Intn(20)+1) * time.Second
}

func main() {
	// Configure the logger
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Create base context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create workers with initial configuration
	worker1, err := NewWorker(WorkerConfig{
		Interval: 5 * time.Second,
		JobName:  "periodic-task-1",
	}, logger)
	if err != nil {
		logger.Error("Failed to create worker 1", "error", err)
		os.Exit(1)
	}

	worker2, err := NewWorker(WorkerConfig{
		Interval: 10 * time.Second,
		JobName:  "periodic-task-2",
	}, logger)
	if err != nil {
		logger.Error("Failed to create worker 2", "error", err)
		os.Exit(1)
	}

	// Config callback that randomizes intervals on reload
	configCallback := func() (*composite.Config[*Worker], error) {
		// Create entries array with our workers
		newEntries := make([]composite.RunnableEntry[*Worker], 2)

		// Update worker1 config with random interval
		newEntries[0] = composite.RunnableEntry[*Worker]{
			Runnable: worker1,
			Config: WorkerConfig{
				Interval: randInterval(),
				JobName:  worker1.config.JobName,
			},
		}

		// Update worker2 config with random interval
		newEntries[1] = composite.RunnableEntry[*Worker]{
			Runnable: worker2,
			Config: WorkerConfig{
				Interval: randInterval(),
				JobName:  worker2.config.JobName,
			},
		}

		logger.Debug("Generated new config for workers")
		return composite.NewConfig("worker-composite", newEntries)
	}

	// Create composite runner
	runner, err := composite.NewRunner(
		configCallback,
		composite.WithContext[*Worker](ctx),
	)
	if err != nil {
		logger.Error("Failed to create composite runner", "error", err)
		os.Exit(1)
	}

	// Create supervisor and add the composite runner
	sv, err := supervisor.New(
		supervisor.WithContext(ctx),
		supervisor.WithRunnables(runner),
	)
	if err != nil {
		logger.Error("Failed to create supervisor", "error", err)
		os.Exit(1)
	}

	// Start the supervisor - this will block until shutdown
	logger.Info("Starting supervisor with composite runner")
	logger.Info("Send this process a HUP signal to reload the configuration")
	logger.Info("Press Ctrl+C to quit")
	logger.Info(strings.Repeat("-", 79))

	if err := sv.Run(); err != nil {
		logger.Error("Supervisor failed", "error", err)
		os.Exit(1)
	}
}
