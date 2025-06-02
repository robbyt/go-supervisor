# HTTP Server Runnable

A production-ready HTTP server that integrates with go-supervisor for lifecycle management.

## Why Use This?

Building HTTP servers that handle shutdown gracefully is complex. When a process receives a termination signal, it needs to stop accepting new connections while allowing in-flight requests to complete. This package solves that problem by providing an HTTP server that:

- Integrates with go-supervisor's shutdown coordination
- Reloads configuration without dropping connections
- Reports its internal state for monitoring

## Basic Usage

```go
package main

import (
    "context"
    "fmt"
    "net/http"
    "time"

    "github.com/robbyt/go-supervisor"
    "github.com/robbyt/go-supervisor/runnables/httpserver"
)

func main() {
    // Create HTTP handlers
    indexHandler := func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Welcome to the home page!")
    }
    
    statusHandler := func(w http.ResponseWriter, r *http.Request) {
        fmt.Fprintf(w, "Status: OK")
    }
    
    // Create routes from standard HTTP handlers
    indexRoute, _ := httpserver.NewRouteFromHandlerFunc("index", "/", indexHandler)
    statusRoute, _ := httpserver.NewRouteFromHandlerFunc("status", "/status", statusHandler)
    
    routes := httpserver.Routes{*indexRoute, *statusRoute}
    
    // Create config callback
    configCallback := func() (*httpserver.Config, error) {
        return httpserver.NewConfig(":8080", routes, httpserver.WithDrainTimeout(5*time.Second))
    }
    
    // Create HTTP server runner
    hRunner, _ := httpserver.NewRunner(
        httpserver.WithConfigCallback(configCallback),
    )
    
    // create a supervisor instance and add the runner
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    super, _ := supervisor.New(
        supervisor.WithRunnables(hRunner),
        supervisor.WithContext(ctx),
    )
    
    // blocks until the supervisor receives a signal
    if err := super.Run(); err != nil {
        // Handle error appropriately
        panic(err)
    }
}
```

## Middleware

The HTTP server uses a middleware pattern where handlers process requests in a chain. Middleware can intercept requests, modify responses, or abort processing.

### Why Middleware Order Matters

Middleware forms a processing pipeline where each step can affect subsequent steps. The order determines both request flow (first → last) and response flow (last → first).

```go
// Recovery must be first to catch panics from all subsequent middleware
// Headers set early ensure they're present even if later middleware fails
// Logging captures the actual request after security filtering
// Content headers set last prevent handlers from overriding them

middlewares := []httpserver.HandlerFunc{
    recovery.New(lgr),        // Catches panics - must wrap everything
    headersMw.Security(),     // Security headers - always applied
    logger.New(lgr),          // Logs what actually gets processed
    metrics.New(),            // Measures performance
    headersMw.JSON(),         // Sets content type - easily overridden
}
```

### Creating Custom Middleware

Middleware receives a `RequestProcessor` that controls request flow:

```go
package mymiddleware

import (
    "net/http"
    "time"
    "github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a timing middleware
func New() httpserver.HandlerFunc {
    return func(rp *httpserver.RequestProcessor) {
        start := time.Now()
        
        // Continue processing
        rp.Next()
        
        // Measure after request completes
        duration := time.Since(start)
        rp.Writer().Header().Set("X-Process-Time", duration.String())
    }
}
```

The `RequestProcessor` provides:
- `Next()` - Continue to the next handler
- `Abort()` - Stop processing
- `Request()` - Access the HTTP request
- `Writer()` - Access the response writer

## Configuration Reloading

The server implements hot reloading for configuration changes. When the supervisor receives SIGHUP or `Reload()` is called, the server:

1. Fetches new configuration via the callback
2. Updates routes without dropping connections
3. Restarts the server only if the listen address changes

This enables zero-downtime updates for most configuration changes.

## State Monitoring

The server reports its lifecycle state through the `Stateable` interface. States progress through:

- "New" → "Booting" → "Running" → "Stopping" → "Stopped"

Error states can occur at any point. Monitor state changes to coordinate with other services or implement health checks:

```go
stateChan := runner.GetStateChan(ctx)
go func() {
    for state := range stateChan {
        fmt.Printf("HTTP server state: %s\n", state)
    }
}()
```

## Configuration Options

The server supports various timeouts and custom server creation:

```go
// Basic configuration with defaults
config, _ := httpserver.NewConfig(":8080", routes)

// Custom timeouts for different deployment scenarios
config, _ := httpserver.NewConfig(
    ":8080",
    routes,
    httpserver.WithDrainTimeout(10*time.Second),  // Graceful shutdown period
    httpserver.WithReadTimeout(30*time.Second),   // Prevent slow clients
    httpserver.WithWriteTimeout(30*time.Second),  // Prevent slow writes
    httpserver.WithIdleTimeout(2*time.Minute),    // Clean up idle connections
)

// Custom server for TLS or HTTP/2
customServerCreator := func(addr string, handler http.Handler, cfg *httpserver.Config) httpserver.HttpServer {
    return &http.Server{
        Addr:         addr,
        Handler:      handler,
        ReadTimeout:  cfg.ReadTimeout,
        WriteTimeout: cfg.WriteTimeout,
        IdleTimeout:  cfg.IdleTimeout,
        TLSConfig:    &tls.Config{
            MinVersion: tls.VersionTLS12,
        },
    }
}

config, _ := httpserver.NewConfig(
    ":8443",
    routes,
    httpserver.WithServerCreator(customServerCreator),
)
```

## Examples

See `examples/http/` for a basic implementation or `examples/custom_middleware/` for advanced middleware usage.