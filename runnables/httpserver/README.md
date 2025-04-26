# HTTP Server Runnable

This package provides a ready-to-use HTTP server implementation that integrates with the go-supervisor framework. The HTTP server runnable supports configuration reloading, state management, and graceful shutdown.

## Features

- Configurable HTTP routes with path matching
- Middleware support for request processing
- Dynamic route configuration and hot reloading
- Graceful shutdown with configurable timeout
- State reporting and monitoring
- Wildcard route support for catch-all handlers
- Built-in middlewares for common tasks

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
    
    // Create routes
    indexRoute, _ := httpserver.NewRoute("index", "/", indexHandler)
    statusRoute, _ := httpserver.NewRoute("status", "/status", statusHandler)
    
    routes := httpserver.Routes{*indexRoute, *statusRoute}
    
    // Create config callback
    configCallback := func() (*httpserver.Config, error) {
        return httpserver.NewConfig(":8080", 5*time.Second, routes)
    }
    
    // Create HTTP server runner (use functional options pattern)
    runner, _ := httpserver.NewRunner(
        httpserver.WithContext(context.Background()),
        httpserver.WithConfigCallback(configCallback),
    )
    
    // Add to supervisor
    super := supervisor.New([]supervisor.Runnable{runner})
    
    // Boot and run
    super.Boot()
    super.Exec()
}
```

## Using Middleware

The HTTP server supports middleware chains for request processing:

```go
// Import the middleware package
import "github.com/robbyt/go-supervisor/runnables/httpserver/middleware"

// Create a route with middleware
route, _ := httpserver.NewRouteWithMiddlewares(
    "index",
    "/",
    indexHandler,
    middleware.LoggingMiddleware,
    middleware.RecoveryMiddleware,
    middleware.MetricsMiddleware,
)
```

### Built-in Middlewares

All middlewares are in the `middleware` package (`runnables/httpserver/middleware`):

- `LoggingMiddleware`: Logs request method, path, status code, and duration
- `RecoveryMiddleware`: Recovers from panics in handlers with stack trace logging
- `MetricsMiddleware`: Tracks request counts and response codes
- `StateMiddleware`: Adds server state information to response headers
- `WildcardMiddleware`: Provides prefix-based routing for catch-all handlers

All middleware components have 100% test coverage and follow a consistent design pattern with wrapped response writers.

### Custom Middleware

You can create custom middleware functions following the same pattern as the built-in middlewares:

```go
import (
    "net/http"
    
    "github.com/robbyt/go-supervisor/runnables/httpserver/middleware"
)

// For simple middleware without response writer wrapping
func MyCustomMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Do something before request handling
        next(w, r)
        // Do something after request handling
    }
}

// For middleware that needs to capture response details
func MyAdvancedMiddleware(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        // Wrap the response writer to capture status code and written bytes
        rw := middleware.NewResponseWriter(w)
        
        // Do something before request handling
        next(rw, r)
        
        // After handling, we can access status code and written bytes
        statusCode := rw.Status()
        bytesWritten := rw.BytesWritten()
        
        // ...
    }
}
```

## Configuration Reloading

The HTTP server supports hot reloading of configuration, allowing you to update routes without restarting:

```go
// Trigger a reload
if runner, ok := service.(*httpserver.Runner); ok {
    runner.Reload()
}
```

## State Management

The HTTP server implements the `Stateable` interface and reports its state as one of:
- "New"
- "Booting"
- "Running"
- "Stopping"
- "Stopped"
- "Error"

You can monitor state changes:

```go
stateChan := runner.GetStateChan(ctx)
go func() {
    for state := range stateChan {
        fmt.Printf("HTTP server state: %s\n", state)
    }
}()
```

## Configuration Options

The HTTP server runner uses a functional options pattern - see godoc for complete documentation of each option. Common usage patterns:

```go
// Dynamic configuration with a callback function (supports hot reloading)
runner, _ := httpserver.NewRunner(
    httpserver.WithContext(context.Background()),
    httpserver.WithConfigCallback(configCallback),
)

// Static configuration (simpler when hot reloading isn't needed)
config, _ := httpserver.NewConfig(":8080", 5*time.Second, routes)
runner, _ := httpserver.NewRunner(
    httpserver.WithContext(context.Background()),
    httpserver.WithConfig(config),
)
```

## Full Example

See `examples/http/main.go` for a complete example.