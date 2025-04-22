# Composite Runner Example

This example demonstrates how to use the CompositeRunner to manage multiple Worker instances as a single unit. It showcases:

1. Creating a custom Worker type that implements the required interfaces
2. Setting up a CompositeRunner with multiple Worker instances 
3. Handling configuration updates through the ReloadWithConfig interface
4. Implementing a clean and simple configCallback function
5. Properly handling ticker-based periodic tasks with dynamic configuration

## Key Components

### Worker

The `Worker` type is a simple implementation of a background job that:

- Implements the `Runnable` interface with `Run()` and `Stop()`
- Implements the `ReloadableWithConfig` interface to receive new configurations
- Performs periodic work based on a configurable interval
- Uses mutexes to safely handle concurrent access to shared resources
- Handles dynamic ticker interval updates without stopping the worker
- Uses a channel-based approach for reliable tick management

### CompositeRunner

The example demonstrates how to:

- Create a type-safe CompositeRunner for Worker instances
- Use a simple configCallback function to generate new configurations
- Associate workers with their configurations
- Handle hot reload of configurations without service interruption
- Manage the lifecycle of multiple workers as a single entity

## Running the Example

```bash
go run .
```

The example will:

1. Start two Worker instances with different configurations (2s and 5s intervals)
2. Log each worker's activity
3. Allow for configuration reloads through the supervisor
4. Run until you press Ctrl+C to stop

## Testing

The tests demonstrate how to verify:

- That Workers handle configuration reloads properly
- That Workers execute correctly with thread safety
- That the CompositeRunner handles membership changes properly

Run the tests with race detection:

```bash
go test -race -v
```