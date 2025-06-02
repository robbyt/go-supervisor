# Custom Middleware Example

This example demonstrates how to create custom middleware for the httpserver package.

## What It Shows

- Creating a custom middleware that transforms HTTP responses
- Using built-in middleware from the httpserver package
- Proper middleware ordering and composition
- Separation of concerns between middleware layers

## Key Components

### JSON Enforcer Middleware
A custom middleware that ensures all responses are JSON formatted. Non-JSON responses are wrapped in `{"response": "content"}` while valid JSON passes through unchanged.

### Headers Middleware
Uses the built-in headers middleware to set appropriate Content-Type, CORS, and security headers.

## Running the Example

```bash
cd examples/custom_middleware
go run main.go
```

The server starts on `:8081` with several endpoints to demonstrate the middleware behavior.

## Endpoints

- `GET /` - Returns plain text (wrapped in JSON)
- `GET /api/data` - Returns JSON (preserved as-is)  
- `GET /html` - Returns HTML (wrapped in JSON)
- `GET /error` - Returns 404 error (wrapped in JSON)
- `GET /panic` - Triggers panic recovery middleware

## Middleware Ordering

The example demonstrates why middleware order matters:

1. **Recovery** - Must be first to catch panics
2. **Security** - Set security headers early
3. **Logging** - Log all requests
4. **Metrics** - Collect request metrics
5. **Headers** - Set response headers before handler

See the code comments in `main.go` for detailed explanations.