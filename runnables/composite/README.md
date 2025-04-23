# Composite Runner

The Composite Runner manages multiple runnables of the same type as a single unit. It implements the `Runnable`, `Reloadable`, and `Stateable` interfaces, allowing it to be used directly with the go-supervisor package.

## Features

- Manage multiple runnables as a single unit
- Each runnable can have its own configuration
- Type-safe, generic implementation
- Supports both `Reload()` and `ReloadWithConfig(config)`
- Proper lifecycle management (start in order, stop in reverse)
- Error propagation from child runnables
- Dynamic membership changes during reloads (go-supervisor does not support dynamic membership changes, but the CompositeRunner does)

## Quick Start Example

```go
// Create runnable entries with their configs
entries := []composite.RunnableEntry[*myapp.SomeRunnable]{
    {Runnable: runnable1, Config: map[string]any{"timeout": 10 * time.Second}},
    {Runnable: runnable2, Config: map[string]any{"maxConnections": 100}},
}

// Define a config callback
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

// Use with supervisor
super, err := supervisor.New(supervisor.WithRunnables(runner))
if err != nil {
    log.Fatalf("Failed to create supervisor: %v", err)
}

if err := super.Run(); err != nil {
    log.Fatalf("Supervisor failed: %v", err)
}
```

### Shared Configuration Example

If all runnables use the same configuration:

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

## Configuration Callback

The config callback function is central to the CompositeRunner's operation. It is called:
- During initialization
- On reloads
- Whenever the runner needs the current configuration

Your callback should always return the most current configuration. The runner compares configurations to determine if membership has changed during reloads.

## ReloadableWithConfig Interface

For easier reloads, implement the `ReloadableWithConfig` interface in your sub-runnables. This interface makes it simple for the composite runnable to send config updates to sub-runnables during Reload.

In your sub-runnable, implement the `ReloadWithConfig` method. This will take priority over the standard `Reload()` method.
```go
type ConfigurableRunnable struct {}

func (r *ConfigurableRunnable) ReloadWithConfig(config any) {
    if cfg, ok := config.(map[string]any); ok {
        if timeout, ok := cfg["timeout"].(time.Duration); ok {
            r.timeout = timeout
        }
        // ... handle other config parameters
    }
}
```

## Monitoring Child States

You can inspect the states of individual child runnables:

```go
states := cRunner.GetChildStates()
for name, state := range states {
    fmt.Printf("Runnable %s is in state %s\n", name, state)
}
```

## Error Handling

Errors from child runnables are propagated to the CompositeRunner's `Run()` method:

```go
if err := cRunner.Run(ctx); err != nil {
    // This error could be from any child runnable
    log.Fatalf("Runner failed: %v", err)
}
```

Child runnables that return `context.Canceled` or `context.DeadlineExceeded` during shutdown are not reported as errors.

## Best Practices

- **Implement String() carefully**: The CompositeRunner uses the String() method of child runnables to identify them during reloads. Ensure this method returns a consistent, unique identifier.
- **Keep config callbacks fast**: The config callback is called frequently and should return quickly.
- **Consider startup/shutdown order**: Components are started in order, and stopped in reverse order of their definition. Startup and shutdown is concurrent.
- **All runnables will be restarted if membership changes**: If the membership of the composite changes, all runnables will be restarted. This is a design choice to ensure consistency across the group.

---

For more advanced usage and implementation details, see the source code and examples in the repository.