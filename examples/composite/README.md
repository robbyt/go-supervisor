# Composite Runner Example

This example demonstrates how to use the CompositeRunner to manage multiple Worker instances as a single logical unit. It provides a complete implementation you can use as a reference for your own applications.

## What This Example Shows

1. Creating a Worker type that implements both `Runnable` and `ReloadableWithConfig` interfaces
2. Managing multiple Worker instances with a Composite Runner 
3. Handling dynamic configuration updates through the `ReloadWithConfig` method
4. Implementing a thread-safe periodic task with proper context cancellation
5. Coordinating multiple workers' lifecycles through a common supervisor

## Key Components

### Worker Implementation

The `Worker` type (`worker.go`) demonstrates:

```go
// Worker implements both Runnable and ReloadableWithConfig
type Worker struct {
    name       string
    mu         sync.RWMutex
    config     WorkerConfig
    nextConfig chan WorkerConfig
    // ... other fields
}

// Run starts the worker's main loop
func (w *Worker) Run(ctx context.Context) error {
    // Implementation handles context cancellation and config updates
}

// Stop signals the worker to gracefully shut down
func (w *Worker) Stop() {
    // Implementation ensures clean shutdown
}

// ReloadWithConfig receives configuration updates
func (w *Worker) ReloadWithConfig(config any) {
    // Implementation handles type conversion and validation
}
```

### Composite Configuration

The example (`main.go`) demonstrates creating a composite runner:

```go
// Create workers with initial configuration
worker1, err := NewWorker(WorkerConfig{...})
worker2, err := NewWorker(WorkerConfig{...})

// Define config callback that returns configuration for both workers
configCallback := func() (*composite.Config[*Worker], error) {
    newEntries := []composite.RunnableEntry[*Worker]{
        {Runnable: worker1, Config: WorkerConfig{...}},
        {Runnable: worker2, Config: WorkerConfig{...}},
    }
    return composite.NewConfig("worker-composite", newEntries)
}

// Create composite runner with our callback
runner, err := composite.NewRunner(
    composite.WithContext[*Worker](ctx),
    composite.WithConfigCallback(configCallback),
)
```

## Running the Example

```bash
go run .
```

When you run the example:

1. Two Worker instances start with different interval configurations (5s and 10s)
2. Each worker performs periodic tasks at its configured interval
3. You can send a SIGHUP signal to trigger configuration reload (random intervals)
4. Workers smoothly transition to new intervals without stopping
5. Press Ctrl+C to trigger graceful shutdown

## Key Patterns Demonstrated

- **Graceful Shutdown**: Proper context cancellation and resource cleanup
- **Configuration Management**: Thread-safe handling of configuration updates
- **Dynamic Reconfiguration**: Changing intervals without restarting workers
