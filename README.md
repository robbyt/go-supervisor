# go-supervisor

[![Go Reference](https://pkg.go.dev/badge/github.com/robbyt/go-supervisor.svg)](https://pkg.go.dev/github.com/robbyt/go-supervisor)
[![Go Report Card](https://goreportcard.com/badge/github.com/robbyt/go-supervisor)](https://goreportcard.com/report/github.com/robbyt/go-supervisor)
[![Coverage](https://sonarcloud.io/api/project_badges/measure?project=robbyt_go-supervisor&metric=coverage)](https://sonarcloud.io/summary/new_code?id=robbyt_go-supervisor)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A service supervisor for Go applications that manages lifecycle for multiple services. Handles graceful shutdown with OS signal support (SIGINT, SIGTERM), configuration hot reloading (SIGHUP), and state monitoring. Service capabilities are added by implementing optional interfaces.

## Features

- **Service Lifecycle Management**: Start, stop, and monitor multiple services
- **Graceful Shutdown**: Handle OS signals (SIGINT, SIGTERM) for clean termination
- **Hot Reloading**: Reload service configurations with SIGHUP or programmatically
- **State Monitoring**: Track and query the state of running services
- **Context Propagation**: Pass context through service lifecycle for proper cancellation
- **Structured Logging**: Integrated with Go's `slog` package
- **Flexible Configuration**: Functional options pattern for easy customization

## Installation

```bash
go get github.com/robbyt/go-supervisor
```

## Quick Start

Define a runnable service by implementing the Runnable interface with `Run(ctx context.Context) error` and `Stop()` methods. Additional capabilities (Reloadable, Stateable) can be implemented as needed. See `supervisor/interfaces.go` for interface details.

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "time"

    "github.com/robbyt/go-supervisor/supervisor"
)

// Example service that implements Runnable interface
type MyService struct {
    name string
}

// Interface guard, ensuring that MyService implements Runnable
var _ supervisor.Runnable = (*MyService)(nil)

func (s *MyService) Run(ctx context.Context) error {
    fmt.Printf("%s: Starting\n", s.name)
    
    ticker := time.NewTicker(1 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            fmt.Printf("%s: Context canceled\n", s.name)
            return nil
        case <-ticker.C:
            fmt.Printf("%s: Tick\n", s.name)
        }
    }
}

func (s *MyService) Stop() {
    fmt.Printf("%s: Stopping\n", s.name)
    // Perform cleanup if needed
}

func (s *MyService) String() string {
    return s.name
}

func main() {
    // Create some services
    service1 := &MyService{name: "Service1"}
    service2 := &MyService{name: "Service2"}
    
    // Create a custom logger
    handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelDebug,
    })
    
    // Create a supervisor with our services and custom logger
    super, err := supervisor.New(
        supervisor.WithRunnables(service1, service2),
        supervisor.WithLogHandler(handler),
    )
    if err != nil {
        fmt.Printf("Error creating supervisor: %v\n", err)
        os.Exit(1)
    }
    
    // Blocking call to Run(), starts listening to signals and starts all Runnables
    if err := super.Run(); err != nil {
        fmt.Printf("Error: %v\n", err)
        os.Exit(1)
    }
}
```

## Core Interfaces

The package is built around the following interfaces. A "Runnable" is any service that can be
started and stopped, while "Reloadable" and "Stateable" services can be reloaded or report
their state, respectively. The supervisor will discover the capabilities of each service
and manage them accordingly.

```go
// Runnable represents a service that can be run and stopped.
// Run blocks until the service exits; Stop blocks until Run returns.
type Runnable interface {
    fmt.Stringer
    Run(ctx context.Context) error
    Stop()
}

// Reloadable represents a service that can be reloaded.
// Reload blocks until the reload completes (or aborts via ctx).
type Reloadable interface {
    Reload(ctx context.Context)
}

// Stateable represents a service that can report its state.
// Embeds Readiness so the supervisor can wait for startup completion.
type Stateable interface {
    Readiness
    GetState() string
    GetStateChan(context.Context) <-chan string
}

// Readiness reports whether the service has finished its startup phase.
type Readiness interface {
    IsRunning() bool
}

// ReloadSender lets a service trigger reloads from inside.
type ReloadSender interface {
    GetReloadTrigger() <-chan struct{}
}

// ShutdownSender lets a service trigger supervisor-wide shutdown from inside.
type ShutdownSender interface {
    GetShutdownTrigger() <-chan struct{}
}
```

Capabilities are detected by interface assertion — implement only what you need.

### Supervisor options

- `WithRunnables(...)` — register the services to manage.
- `WithContext(ctx)` — provide a parent context for cancellation.
- `WithLogHandler(h)` — install a custom `slog.Handler`.
- `WithSignals(...)` — override the OS signals to listen for. Only `SIGINT`,
  `SIGTERM`, and `SIGHUP` are special-cased; other signals are logged and
  ignored.
- `WithStartupTimeout(d)` — bound how long a runnable's `IsRunning()` may take
  to return true before the supervisor gives up on startup.
- `WithShutdownTimeout(d)` — TOTAL wall-clock budget for graceful shutdown,
  shared between per-runnable `Stop()` calls and the final goroutine wait. A
  runnable whose `Stop()` overruns the remaining budget is abandoned (logged
  warning). `0` disables the deadline.

## Advanced Usage

### Implementing a Reloadable Service

```go
type ConfigurableService struct {
    MyService
    config *Config
    mu     sync.Mutex
}

// Interface guards, ensuring that ConfigurableService implements Runnable and Reloadable
var _ supervisor.Runnable = (*ConfigurableService)(nil)
var _ supervisor.Reloadable = (*ConfigurableService)(nil)

type Config struct {
    Interval time.Duration
}

func (s *ConfigurableService) Reload(ctx context.Context) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Load new config from file or environment
    newConfig := loadConfig()
    s.config = newConfig
    
    fmt.Printf("%s: Configuration reloaded\n", s.name)
}
```

## Example Runnables

The package includes the following runnable implementations:

- **HTTP Server**: A configurable HTTP server with routing and middleware support (`runnables/httpserver`)
- **Composite**: A container for managing multiple Runnables using generics (`runnables/composite`)
- **HTTP Cluster**: Dynamic management of multiple HTTP servers with hot-reload support using channel-based configuration (`runnables/httpcluster`)

Each runnable has its own documentation in its directory (e.g., `runnables/httpserver/README.md`).

## License

Apache License 2.0 - See [LICENSE](LICENSE) for details.
