# HTTP Cluster Example

This example demonstrates how to use the `httpcluster` runnable with the go-supervisor to manage multiple HTTP servers dynamically.

## Overview

The example shows an interactive HTTP server that can reconfigure itself to listen on different ports based on POST requests. The server starts on port 8080 and can be instructed to move to a different port by sending a JSON payload.

## Running the Example

```bash
# From the examples/httpcluster directory
go run main.go
```

The server will start on port 8080. Visit http://localhost:8080 to see instructions.

## Using the Example

1. **Check current status**:
   ```bash
   curl http://localhost:8080/status
   ```

2. **Change the port**:
   ```bash
   curl -X POST http://localhost:8080/ \
     -H 'Content-Type: application/json' \
     -d '{"port":":8081"}'
   ```

3. **Verify the server moved**:
   ```bash
   curl http://localhost:8081/status
   ```

## Key Features Demonstrated

1. **Dynamic Configuration**: Server port can be changed at runtime
2. **Channel-based Updates**: Configuration updates are sent through a channel
3. **Supervisor Integration**: The cluster is managed by go-supervisor for signal handling
4. **State Monitoring**: The example logs all state changes
5. **Middleware**: Each server uses logging, recovery, and metrics middleware