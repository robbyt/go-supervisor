# HTTP Cluster Runnable

## Overview

The `httpcluster` Runnable manages multiple HTTP server instances dynamically as a cluster. It uses a channel-based "siphon" pattern for configuration updates.

## Key Differences from Other Runnables

| Feature | httpserver | composite | httpcluster |
|---------|------------|-----------|-------------|
| **Purpose** | Single HTTP server | Generic Runnable container | Multiple HTTP servers |
| **Scope** | One server instance | Any Runnable types | HTTP servers only |
| **Config Model** | Callback (pull) | Callback (pull) | Channel (push) |
| **Implements supervisor.Reloadable** | Yes | Yes | No |
| **Config Changes** | Updates routes | Updates Runnables | Add/remove servers |
| **Port Management** | Single port | N/A | Multiple ports |

## Features

- **Dynamic Server Management**: Add, remove, or reconfigure HTTP servers at runtime
- **Siphon Channel Pattern**: Push-based configuration updates via channel
- **State Aggregation**: Unified state reporting across all managed servers
- **2-Phase Commit**: Configuration updates with rollback capability
- **Stop-Then-Start Updates**: All configuration changes stop old servers before starting new ones
- **Context Hierarchy**: Context propagation for cancellation
- **Thread Safety**: Concurrent operations with immutable state management

## Basic Usage

```go
package main

import (
    "log"
    "net/http"
    
    "github.com/robbyt/go-supervisor/supervisor"
    "github.com/robbyt/go-supervisor/runnables/httpcluster"
    "github.com/robbyt/go-supervisor/runnables/httpserver"
)

func main() {
    // Create the httpcluster Runnable
    cluster, err := httpcluster.NewRunner()
    if err != nil {
        log.Fatal(err)
    }
    
    // Create supervisor with the cluster
    super, err := supervisor.New(
        supervisor.WithRunnables(cluster),
    )
    if err != nil {
        log.Fatal(err)
    }
    
    // Send initial configuration
    go func() {
        handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Write([]byte("Hello World"))
        })
        
        route, _ := httpserver.NewRouteFromHandlerFunc("hello", "/", handler)
        config, _ := httpserver.NewConfig(":8080", httpserver.Routes{*route})
        
        cluster.configSiphon <- map[string]*httpserver.Config{
            "server1": config,
        }
    }()
    
    // Run supervisor (blocks until signal)
    if err := super.Run(); err != nil {
        log.Fatal(err)
    }
}
```

## State Management

The cluster implements the `supervisor.Stateable` interface and uses FSM (Finite State Machine) for state management:

- **New**: Initial state, no servers running
- **Booting**: Cluster starting up
- **Running**: Cluster operational, ready for config updates
- **Reloading**: Processing configuration update
- **Stopping**: Graceful shutdown in progress
- **Stopped**: All servers stopped
- **Error**: Unrecoverable error occurred

Monitor state changes:

```go
stateChan := cluster.GetStateChan(ctx)
go func() {
    for state := range stateChan {
        log.Printf("Cluster state: %s", state)
    }
}()
```

## Configuration Options

```go
// Create cluster with options
cluster, err := httpcluster.NewRunner(
    httpcluster.WithSiphonBuffer(10),        // Buffer size for config channel
    httpcluster.WithContext(parentCtx),      // Parent context
    httpcluster.WithLogger(customLogger),    // Custom slog logger
)
```

### Available Options

- `WithSiphonBuffer(size int)`: Sets buffer size for config channel (default: 0 - unbuffered)
- `WithContext(ctx context.Context)`: Sets parent context (default: context.Background())
- `WithLogger(logger *slog.Logger)`: Sets custom logger (default: slog.Default())

## Implementation Details

### 2-Phase Commit Pattern

The cluster uses an immutable 2-phase commit pattern for configuration updates:

1. **Phase 1 - Calculate Changes**: Create new entries with pending actions (start/stop)
2. **Phase 2 - Execute Actions**: Perform all server starts/stops
3. **Commit**: Finalize the new state

This ensures atomic updates and enables rollback if needed.

### Update Strategy

All configuration changes use a "stop-then-start" approach:
- Even for route changes, the old server is stopped and a new one started
- Prevents edge cases with state transitions
- Trade-off: Brief downtime per server during updates

### Context Hierarchy

```
parentCtx (from construction)
    └── runCtx (from Run method)
        ├── serverCtx1 (for api server)
        ├── serverCtx2 (for admin server)
        └── serverCtx3 (for metrics server)
```

## Integration with Supervisor

Unlike composite and httpserver, httpcluster does NOT implement the Reloadable interface. Configuration updates are handled through the siphon channel:

```go
package main

import (
    "log"
    "net/http"
    "time"
    
    "github.com/robbyt/go-supervisor/supervisor"
    "github.com/robbyt/go-supervisor/runnables/httpcluster"
    "github.com/robbyt/go-supervisor/runnables/httpserver"
)

func main() {
    // Create cluster
    cluster, err := httpcluster.NewRunner()
    if err != nil {
        log.Fatal(err)
    }
    
    // Create supervisor
    super, err := supervisor.New(
        supervisor.WithRunnables(cluster),
    )
    if err != nil {
        log.Fatal(err)
    }
    
    // Configuration manager goroutine
    go func() {
        // Initial configuration
        handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Write([]byte("API v1"))
        })
        
        route, _ := httpserver.NewRouteFromHandlerFunc("api", "/api", handler)
        config, _ := httpserver.NewConfig(":8080", httpserver.Routes{*route})
        
        cluster.configSiphon <- map[string]*httpserver.Config{
            "api": config,
        }
        
        // Later: add another server
        time.Sleep(10 * time.Second)
        
        adminHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            w.Write([]byte("Admin"))
        })
        
        adminRoute, _ := httpserver.NewRouteFromHandlerFunc("admin", "/admin", adminHandler)
        adminConfig, _ := httpserver.NewConfig(":9090", httpserver.Routes{*adminRoute})
        
        cluster.configSiphon <- map[string]*httpserver.Config{
            "api":   config,      // Keep existing
            "admin": adminConfig, // Add new
        }
    }()
    
    // Run supervisor (blocks until signal)
    if err := super.Run(); err != nil {
        log.Fatal(err)
    }
}
```

## Design Rationale

### Why Siphon Channel?
The siphon channel pattern was chosen over callbacks because:
- **Push Model**: External systems can push updates when ready
- **Decoupling**: Config source doesn't need to know about the cluster
- **Testability**: Control timing in tests
- **Backpressure**: Unbuffered channel provides flow control

### Why Not Implement Reloadable?
- The Reloadable interface assumes a pull model (callback-based)
- Siphon channel provides a push model for dynamic configs
- Avoids mixing paradigms (push vs pull)
- Suited for event-driven architectures

### Why Stop-Then-Start?
- Simplifies state management 
- Prevents edge cases with in-flight requests
- Clear, predictable behavior
- Trade-off accepted: brief downtime during updates

## Testing

The package includes tests for:
- **Unit tests**: Entries management, state transitions
- **Integration tests**: Full cluster lifecycle, concurrent updates

Run tests:
```bash
go test -race ./runnables/httpcluster/...
```

## See Also

- httpserver - Single HTTP server implementation
- composite - Generic Runnable container
- go-supervisor - Main supervisor framework