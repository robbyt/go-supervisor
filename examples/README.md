# go-supervisor Examples

This directory contains examples demonstrating how to use the go-supervisor package in real-world scenarios. Each example showcases different aspects of service lifecycle management, configuration, and state handling.

## HTTP Server Example

The [http](./http/) directory contains a complete example of an HTTP server managed by the go-supervisor package. This example demonstrates:

- Integration with the `runnables/httpserver` implementation
- Proper configuration of routes and handlers
- Custom logging with slog
- Integration with the PIDZero supervisor
- Signal handling for graceful shutdown

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

When implementing your own service that integrates with go-supervisor, you typically need to:

1. **Implement the Runnable Interface**:
   ```go
   type MyService struct {
       // Your service fields
   }
   
   func (s *MyService) Run(ctx context.Context) error {
       // Start your service
       // Monitor ctx.Done() for cancellation
   }
   
   func (s *MyService) Stop() {
       // Clean shutdown logic
   }
   ```

2. **Optionally Implement Reloadable**:
   ```go
   func (s *MyService) Reload() {
       // Reload configuration
   }
   ```

3. **Optionally Implement Stateable**:
   ```go
   func (s *MyService) GetState() string {
       // Return current state
   }
   
   func (s *MyService) GetStateChan(ctx context.Context) <-chan string {
       // Return channel that emits state changes
   }
   ```

4. **Create and Start a Supervisor**:
   ```go
   super := supervisor.New(
       []supervisor.Runnable{myService},
       supervisor.WithLogHandler(logHandler),
   )
   
   if err := super.Run(); err != nil {
       log.Fatalf("Error: %v", err)
   }
   ```

See the [HTTP example](./http/main.go) for a complete implementation.