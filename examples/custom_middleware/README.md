# JSON API Example

This example demonstrates a JSON API server using the go-supervisor package with two key middlewares:

1. **Headers Middleware** (reusable) - Sets HTTP headers for JSON responses, CORS, and security
2. **JSON Enforcer Middleware** (example) - Ensures all responses are JSON formatted

## Features

- **Automatic JSON Response Conversion**: Non-JSON responses are wrapped in `{"response": "original content"}`
- **JSON Response Preservation**: Valid JSON responses are left unchanged
- **Headers Management**: Automatic JSON content-type, CORS, and security headers
- **Multiple Response Types**: Demonstrates text, JSON, HTML, and error responses
- **Comprehensive Middleware Stack**: Recovery, logging, metrics, headers, and JSON enforcement

## Endpoints

- `GET /` - Plain text response (converted to JSON)
- `GET /api/data` - JSON response (preserved as-is)  
- `GET /html` - HTML response (converted to JSON)
- `GET /error` - Error response (converted to JSON)
- `GET /status` - Service status (JSON)

## Middleware Architecture

### Headers Middleware (`@runnables/httpserver/middleware/headers`)

A reusable middleware package that provides:

```go
// Set JSON headers
headersMw.JSON()

// Set CORS headers
headersMw.CORS("*", "GET,POST,PUT,DELETE,OPTIONS", "Content-Type,Authorization")

// Set security headers
headersMw.Security()

// Add custom headers
headersMw.Add("X-API-Version", "v1.0")
```

### JSON Enforcer Middleware (`@examples/jsonapi/middleware`)

An example middleware that ensures all responses are JSON:

```go
// Wrap handlers to enforce JSON responses
middleware.WrapHandlerForJSON(handler)
```

## Running the Example

```bash
cd examples/jsonapi
go run main.go
```

The server will start on `:8081` and display available endpoints.

## Testing

```bash
cd examples/jsonapi
go test -v ./...
```

Tests cover:
- JSON validation logic
- Response wrapping behavior
- Handler integration
- Middleware composition
- Header management
- Status code preservation

## Example Responses

### Text Response (/)
**Request:** `GET /`
**Response:**
```json
{"response": "Welcome to the JSON API example server!"}
```

### JSON Response (/api/data)  
**Request:** `GET /api/data`
**Response:**
```json
{"message": "This is already JSON", "status": "success", "timestamp": "2024-01-01T12:00:00Z"}
```

### HTML Response (/html)
**Request:** `GET /html`  
**Response:**
```json
{"response": "<html><body><h1>Hello from HTML</h1><p>This will be converted to JSON!</p></body></html>"}
```

### Error Response (/error)
**Request:** `GET /error`
**Response:** (HTTP 404)
```json
{"response": "The requested resource was not found"}
```

## Key Implementation Details

1. **Handler Wrapping**: JSON enforcement happens at the handler level using `WrapHandlerForJSON()`
2. **Response Detection**: Uses JSON validation to determine if content is already JSON
3. **Status Code Preservation**: Maintains original HTTP status codes while converting content
4. **Middleware Composition**: Demonstrates layering multiple middleware types
5. **Header Management**: Shows systematic header application across different route types