# go-supervisor

[![Go Reference](https://pkg.go.dev/badge/github.com/robbyt/go-supervisor.svg)](https://pkg.go.dev/github.com/robbyt/go-supervisor)
[![Go Report Card](https://goreportcard.com/badge/github.com/robbyt/go-supervisor)](https://goreportcard.com/report/github.com/robbyt/go-supervisor)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A lightweight, flexible service supervisor for Go applications. The `go-supervisor` package provides robust lifecycle management for multiple services with support for graceful shutdown, configuration reloading, and state monitoring. It uses a clean interface-based design that makes it easy to implement and test service components.

## Features

- **Service Lifecycle Management**: Start, stop, and monitor multiple services
- **Graceful Shutdown**: Handle OS signals (SIGINT, SIGTERM) for clean termination
- **Hot Reloading**: Reload service configurations with SIGHUP or programmatically
- **State Monitoring**: Track and query the state of running services
- **Context Propagation**: Pass context through service lifecycle for proper cancellation
- **Structured Logging**: Integrated with Go's `slog` package
- **Flexible Configuration**: Functional options pattern for easy customization
- **Finite State Machine**: Integrated with FSM for robust state management
- **Ready-to-use Runnables**: Includes HTTP server implementation

## Installation

```bash
go get github.com/robbyt/go-supervisor
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "time"

    "github.com/robbyt/go-supervisor"
)

// Example service that implements Runnable interface
type MyService struct {
    name string
    done chan struct{}
}

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
    service1 := &MyService{name: "Service1", done: make(chan struct{})}
    service2 := &MyService{name: "Service2", done: make(chan struct{})}
    
    // Create a custom logger
    handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
        Level: slog.LevelDebug,
    })
    
    // Create a supervisor with our services and custom logger
    super := supervisor.New(
        []supervisor.Runnable{service1, service2},
        supervisor.WithHandler(handler),
    )
    
    // Boot the supervisor
    super.Boot()
    
    // Start all services and wait
    if err := super.Exec(); err != nil {
        fmt.Printf("Error: %v\n", err)
        os.Exit(1)
    }
}
```

## Core Interfaces

The package is built around the following interfaces:

```go
// Runnable represents a service that can be run and stopped
type Runnable interface {
    Run(ctx context.Context) error
    Stop()
}

// Reloadable represents a service that can be reloaded
type Reloadable interface {
    Reload()
}

// Stateable represents a service that can report its state
type Stateable interface {
    GetState() string
    GetStateChan(context.Context) <-chan string
}

// ReloadSender represents a service that can trigger reloads
type ReloadSender interface {
    GetReloadTrigger() <-chan struct{}
}
```

## Advanced Usage

### Implementing Reloadable Services

```go
type ConfigurableService struct {
    MyService
    config *Config
    mu     sync.Mutex
}

type Config struct {
    Interval time.Duration
}

func (s *ConfigurableService) Reload() {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // Load new config from file or environment
    newConfig := loadConfig()
    s.config = newConfig
    
    fmt.Printf("%s: Configuration reloaded\n", s.name)
}
```

### Implementing Stateable Services

```go
type StatefulService struct {
    MyService
    state     string
    stateChan chan string
    mu        sync.Mutex
}

func NewStatefulService(name string) *StatefulService {
    return &StatefulService{
        MyService: MyService{name: name, done: make(chan struct{})},
        state:     "initialized",
        stateChan: make(chan string, 10),
    }
}

func (s *StatefulService) GetState() string {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.state
}

func (s *StatefulService) GetStateChan(ctx context.Context) <-chan string {
    return s.stateChan
}

func (s *StatefulService) setState(state string) {
    s.mu.Lock()
    s.state = state
    s.mu.Unlock()
    
    // Non-blocking send to state channel
    select {
    case s.stateChan <- state:
    default:
    }
}

func (s *StatefulService) Run(ctx context.Context) error {
    s.setState("running")
    // Run implementation
    return nil
}

func (s *StatefulService) Stop() {
    s.setState("stopping")
    // Stop implementation
    s.setState("stopped")
}
```

### Custom Signal Handling

```go
super := supervisor.New(
    []supervisor.Runnable{service1, service2},
    supervisor.WithSignals(syscall.SIGINT, syscall.SIGTERM),
)
```

## Advanced Topics

### Error Handling

When a service returns an error from its `Run` method, the supervisor will:

1. Log the error with relevant context
2. Initiate graceful shutdown of all services
3. Return the error from `Exec()`

### Runnables

The package includes ready-to-use runnables for common use cases:

- HTTP Server Runnable: A configurable HTTP server with routing and middleware support

Each runnable has its own documentation in its directory (e.g., `runnables/httpserver/README.md`).

For usage examples, see the `examples/` directory.

### Monitoring Service States

You can query the state of services at any time:

```go
// Get state of a specific service
state := super.GetState(service1)
fmt.Printf("Service1 state: %s\n", state)

// Get states of all services
states := super.GetStates()
for service, state := range states {
    fmt.Printf("%s: %s\n", service, state)
}
```

### Custom Context

You can provide a custom context to the supervisor:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

super := supervisor.NewWithContext(ctx, services, options...)
```

## Testing

The package provides mock implementations of all interfaces for testing:

```go
import (
    "context"
    "testing"
    "time"
    
    "github.com/robbyt/go-supervisor"
    "github.com/robbyt/go-supervisor/runnables/mocks"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
)

func TestMyComponent(t *testing.T) {
    // Create a mock service
    mockService := &mocks.MockService{}
    
    // Set expectations
    mockService.On("Run", mock.Anything).Return(nil)
    mockService.On("Stop").Once()
    
    // Create supervisor with mock
    super := supervisor.New([]supervisor.Runnable{mockService})
    
    // Run test...
    
    // Verify expectations
    mockService.AssertExpectations(t)
}
```

## Development

For development, use the following commands:

```bash
# Run all tests with race detection
make test

# Run linter
make lint

# Fix common linting issues
make lint-fix 

# Run benchmarks
make bench
```

## License

MIT