# Composite Runner

The Composite Runner manages multiple runnables as a single logical unit. This "runnable" implements several `go-supervisor` core interfaces: `Runnable`, `Reloadable`, and `Stateable`.

## Features

- Group and manage multiple runnables as a single service (all the same type)
- Provide individual configuration for each runnable or shared configuration (with hot-reload)
- Support dynamic membership changes during reloads (sub-runnables can be added or removed)
- Propagate errors from child runnables to the supervisor
- Monitor state of individual child runnables
- Manage configuration updates to the sub-runnables with a callback function

## Quick Start Example

```go
// Create runnable entries with their configs
entries := []composite.RunnableEntry[*myapp.SomeRunnable]{
    {Runnable: runnable1, Config: map[string]any{"timeout": 10 * time.Second}},
    {Runnable: runnable2, Config: map[string]any{"maxConnections": 100}},
}

// Define a config callback, used for dynamic membership changes and reloads
configCallback := func() (*composite.Config[*myapp.SomeRunnable], error) {
    return composite.NewConfig("MyRunnableGroup", entries)
}

// Create a composite runner
runner, err := composite.NewRunner(
    composite.WithConfigCallback(configCallback),
)
if err != nil {
    log.Fatalf("Failed to create runner: %v", err)
}

// Load the composite runner into a supervisor
super, err := supervisor.New(supervisor.WithRunnables(runner))
if err != nil {
    log.Fatalf("Failed to create supervisor: %v", err)
}

if err := super.Run(); err != nil {
    log.Fatalf("Supervisor failed: %v", err)
}
```

### Shared Configuration Example

When all runnables share the same configuration:

```go
runnables := []*myapp.SomeRunnable{runnable1, runnable2, runnable3}
configCallback := func() (*composite.Config[*myapp.SomeRunnable], error) {
    return composite.NewConfigFromRunnables(
        "MyRunnableGroup",
        runnables,
        map[string]any{"timeout": 30 * time.Second},
    )
}
runner, err := composite.NewRunner(
    composite.WithConfigCallback(configCallback),
)
```

## Dynamic Configuration

The config callback function provides the configuration for the Composite Runner:

- Returns the current configuration when requested
- Called during initialization and reloads
- Used to determine if runnable membership has changed
- Should return quickly as it may be called frequently

```go
// Example config callback that loads from file
configCallback := func() (*composite.Config[*myapp.SomeRunnable], error) {
    // Read config from file or other source
    config, err := loadConfigFromFile("config.json")
    if err != nil {
        return nil, err
    }
    
    // Create entries based on loaded config
    var entries []composite.RunnableEntry[*myapp.SomeRunnable]
    for name, cfg := range config.Services {
        runnable := getOrCreateRunnable(name)
        entries = append(entries, composite.RunnableEntry[*myapp.SomeRunnable]{
            Runnable: runnable,
            Config:   cfg,
        })
    }
    
    return composite.NewConfig("MyServices", entries)
}
```

## ReloadableWithConfig Interface

Implement the `ReloadableWithConfig` interface to receive type-specific configuration updates:

```go
type ConfigurableRunnable struct {
    timeout time.Duration
    // other fields
}

// Run implements the Runnable interface
func (r *ConfigurableRunnable) Run(ctx context.Context) error {
    // implementation
}

// Stop implements the Runnable interface
func (r *ConfigurableRunnable) Stop() {
    // implementation
}

// ReloadWithConfig receives configuration updates during reloads
func (r *ConfigurableRunnable) ReloadWithConfig(config any) {
    if cfg, ok := config.(map[string]any); ok {
        if timeout, ok := cfg["timeout"].(time.Duration); ok {
            r.timeout = timeout
        }
        // Handle other config parameters
    }
}
```

The Composite Runner will prioritize calling `ReloadWithConfig` over the standard `Reload()` method when a runnable implements both.

## Monitoring Child States

Monitor the states of individual child runnables:

```go
// Get a map of all child runnable states
states := compositeRunner.GetChildStates()

// Log the current state of each runnable
for name, state := range states {
    logger.Infof("Service %s is in state %s", name, state)
}

// Check if a specific service is ready
if states["database"] == "running" {
    // Database service is ready
}
```

## Managing Lifecycle

The Composite Runner coordinates the lifecycle of all contained runnables:

- Starts runnables in the order they are defined (async)
- Stops runnables in reverse order
- Propagates errors from any child runnable
- Handles clean shutdown when context is canceled
- Manages state transitions (New → Booting → Running → Stopping → Stopped)

## Best Practices

- **Unique Identifiers**: Ensure each runnable's `String()` method returns a consistent, unique identifier
- **Stateful Configuration**: Store your latest configuration for reuse if the config source becomes temporarily unavailable
- **Error Handling**: Check errors returned from `Run()` to detect failures in any child runnable
- **Context Management**: Pass a cancellable context to `Run()` for controlled shutdown
- **Membership Changes**: Be aware that changes in membership will cause all runnables to restart
- **Type Safety**: Use the same concrete type for all runnables in a composite to leverage Go's type system

---

See the [examples directory](../examples/composite/) for complete working examples.