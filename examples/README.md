# go-supervisor Examples

This directory contains examples demonstrating how to use the go-supervisor package in real-world scenarios. Each example showcases different aspects of service lifecycle management, configuration, and state handling.

> **Note:** More examples are coming soon as we add additional runnable implementations to the project. Currently, the HTTP server example demonstrates a complete implementation of the supervisor interfaces.

## HTTP Server Example

The [http](./http/) directory contains a complete example of an HTTP server managed by the go-supervisor package.

This example demonstrates:
- Creating a `runnables.HttpServer`, implementing `supervisor.PIDZero` interfaces (Runnable, Reloadable, Stateable)
- Configuration of routes and handlers, from the `httpserver` package
- Logging with an `slog.Handler`

Key components of the HTTP server example:

1. **Route Configuration**: Shows how to create standard and wildcard routes
2. **Configuration Callback**: Demonstrates dynamic configuration loading
3. **State Management**: Uses FSM-based state tracking for the server lifecycle
4. **Supervisor Integration**: Properly initializes and uses the PIDZero supervisor

To run the HTTP server example:

```bash
cd examples/http
go build
./http
```

Then access the server at:
- `http://localhost:8080/` - Index route
- `http://localhost:8080/status` - Status endpoint 
- `http://localhost:8080/api/anything` - Wildcard API route

Press Ctrl+C to trigger a graceful shutdown.

## Implementing Your Own Runnable
For detailed instructions on implementing your own runnable, please refer to the root-level README. The HTTP example in this directory provides a complete implementation that you can use as a reference.

See the [HTTP example](./http/main.go) for a complete implementation.