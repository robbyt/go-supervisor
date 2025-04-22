# Composite Runner

The Composite Runner is a component that allows multiple instances of the same Runnable type to be managed as a single unit. It implements the core `Runnable` interface as well as the `Reloadable` and `Stateable` interfaces from the go-supervisor package.

## Features

- Manage multiple runnables of the same type as a single unit
- Each runnable has its own configuration for targeted reloads
- Generic implementation ensures type safety
- Support for both standard Reload() and enhanced ReloadWithConfig(config) patterns
- Proper lifecycle management (start front-to-back, stop back-to-front)
- Thread-safe operations with RWMutex protection
- Independent state tracking through FSM
- Automatic error propagation from child runnables

## Usage

### Basic Usage

```go
// Create runnable entries with their configs
entries := []composite.RunnableEntry[*myapp.SomeRunnable]{
    {
        Runnable: runnable1,
        Config:   map[string]any{"timeout": 10 * time.Second},
    },
    {
        Runnable: runnable2,
        Config:   map[string]any{"maxConnections": 100},
    },
}

// Create a configuration callback
configCallback := func() (*composite.Config[*myapp.SomeRunnable], error) {
    return composite.NewConfig("MyRunnableGroup", entries)
}

// Create a new composite runner
runner, err := composite.NewRunner(
    composite.WithConfigCallback(configCallback),
    composite.WithLogHandler(customLogHandler),
)
if err != nil {
    // Handle error
}

// Use the runner with a supervisor
super, err := supervisor.New(
    supervisor.WithRunnables(runner),
)
if err != nil {
    // Handle error
}

// Run the supervisor
if err := super.Run(); err != nil {
    // Handle error
}
```

### Alternative Usage with Shared Configuration

If all runnables use the same configuration, you can use the helper function:

```go
// Create a list of runnables
runnables := []*myapp.SomeRunnable{runnable1, runnable2, runnable3}

// Create a configuration callback using the helper function
configCallback := func() (*composite.Config[*myapp.SomeRunnable], error) {
    return composite.NewConfigFromRunnables(
        "MyRunnableGroup",
        runnables,
        map[string]any{"timeout": 30 * time.Second}
    )
}

// Create a new composite runner
runner, err := composite.NewRunner(
    composite.WithConfigCallback(configCallback),
)
```

## ReloadableWithConfig Interface

For more granular control over reloading, implement the `ReloadableWithConfig` interface:

```go
// Define a runnable that supports configuration-based reloading
type ConfigurableRunnable struct {
    // ... fields
}

// Implement standard Reload() method
func (r *ConfigurableRunnable) Reload() {
    // Default reload behavior
}

// Implement enhanced ReloadWithConfig() method
func (r *ConfigurableRunnable) ReloadWithConfig(config any) {
    // Type assert to expected config type
    if cfg, ok := config.(map[string]any); ok {
        if timeout, ok := cfg["timeout"].(time.Duration); ok {
            r.timeout = timeout
        }
        // ... handle other config parameters
    }
    // Apply the new configuration
}
```

The Composite Runner checks if each child runnable implements this interface. If it does, `ReloadWithConfig()` is called with the runnable's specific configuration. Otherwise, it falls back to the standard `Reload()` method.

## Child State Inspection

You can inspect the states of individual child runnables:

```go
// Get states of all child runnables
states := runner.GetChildStates()
for name, state := range states {
    fmt.Printf("Runnable %s is in state %s\n", name, state)
}
```