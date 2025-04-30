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
    
    // Create HTTP server runner
    hRunner, _ := httpserver.NewRunner(
        httpserver.WithConfigCallback(configCallback),
    )
    
    // create a supervisor instance and add the runner
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    super, _ := supervisor.New(
        supervisor.WithRunnables(hRunner), // Remove the spread operator '...'
        supervisor.WithContext(ctx),
    )
    
    // blocks until the supervisor receives a signal
    if err := super.Run(); err != nil {
        // Handle error appropriately
        panic(err)
    }
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

The HTTP server runnable implements the `supervisor.Reloadable` interface. The `go-supervisor`
instance managing this runnable will automatically trigger its `Reload()` method when the
supervisor receives a `SIGHUP` signal.

When `Reload()` is called (either by the supervisor via a HUP signal or programmatically), the
runner calls the configuration callback function to fetch the latest configuration. If the
configuration has changed, the underlying HTTP server may be gracefully shut down if ports changed,
or the routes will be reloaded if the configuration is the same.


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
config, _ := httpserver.NewConfig(
    ":8080",            // Listen address
    routes,              // HTTP routes
    httpserver.WithDrainTimeout(5*time.Second), // Optional settings
)
runner, _ := httpserver.NewRunner(
    httpserver.WithContext(context.Background()),
    httpserver.WithConfig(config),
)
```

## Server Configuration with Functional Options

The HTTP server config uses the Functional Options pattern, which provides a clean and flexible way to configure server settings. The pattern allows for sensible defaults while making it easy to customize specific settings.

### Basic Configuration

The `NewConfig` function now accepts an address, routes, and optional configuration options:

```go
// Create a config with just the required parameters (uses default timeouts)
config, _ := httpserver.NewConfig(":8080", routes)

// Create a config with custom drain timeout
config, _ := httpserver.NewConfig(
    ":8080",            // Listen address
    routes,              // HTTP routes
    httpserver.WithDrainTimeout(10*time.Second),
)

// Create a config with multiple custom settings
config, _ := httpserver.NewConfig(
    ":8080",
    routes,
    httpserver.WithDrainTimeout(10*time.Second),
    httpserver.WithReadTimeout(30*time.Second),
    httpserver.WithWriteTimeout(30*time.Second),
    httpserver.WithIdleTimeout(2*time.Minute),
)
```

### Available Configuration Options

The following functional options can be used with `NewConfig`:

- `WithDrainTimeout(timeout time.Duration)`: Sets the drain timeout for graceful shutdown
- `WithReadTimeout(timeout time.Duration)`: Sets the read timeout for the HTTP server
- `WithWriteTimeout(timeout time.Duration)`: Sets the write timeout for the HTTP server
- `WithIdleTimeout(timeout time.Duration)`: Sets the idle connection timeout for the HTTP server
- `WithServerCreator(creator ServerCreator)`: Sets a custom server creator function

### Custom Server Creator

You can provide a custom server creation function to configure advanced HTTP server settings:

```go
// Create a custom server creator
customServerCreator := func(addr string, handler http.Handler, cfg *httpserver.Config) httpserver.HttpServer {
    return &http.Server{
        Addr:         addr,
        Handler:      handler,
        ReadTimeout:  cfg.ReadTimeout,
        WriteTimeout: cfg.WriteTimeout,
        IdleTimeout:  cfg.IdleTimeout,
        // Add any additional custom settings here
    }
}

// Use the custom server creator in the config
config, _ := httpserver.NewConfig(
    ":8080",
    routes,
    httpserver.WithServerCreator(customServerCreator),
)
```

### TLS Configuration Example

```go
// Create a server with TLS configuration
tlsServerCreator := func(addr string, handler http.Handler, cfg *httpserver.Config) httpserver.HttpServer {
    return &http.Server{
        Addr:         addr,
        Handler:      handler,
        ReadTimeout:  cfg.ReadTimeout,
        WriteTimeout: cfg.WriteTimeout,
        IdleTimeout:  cfg.IdleTimeout,
        TLSConfig: &tls.Config{
            MinVersion: tls.VersionTLS12,
            CurvePreferences: []tls.CurveID{
                tls.CurveP256,
                tls.X25519,
            },
        },
    }
}

// Use the TLS server creator
runner, _ := httpserver.NewRunner(
    httpserver.WithConfigCallback(configCallback),
    httpserver.WithServerCreator(tlsServerCreator),
)
```

### HTTP/2 Support Example

```go
// Create a server with HTTP/2 support
http2ServerCreator := func(addr string, handler http.Handler) httpserver.HttpServer {
    server := &http.Server{
        Addr:    addr,
        Handler: handler,
    }
    
    // Enable HTTP/2 support
    http2.ConfigureServer(server, &http2.Server{})
    
    return server
}

// Use the HTTP/2 server creator
runner, _ := httpserver.NewRunner(
    httpserver.WithConfigCallback(configCallback),
    httpserver.WithServerCreator(http2ServerCreator),
)
```

## Full Example

See `examples/http/main.go` for a complete example.