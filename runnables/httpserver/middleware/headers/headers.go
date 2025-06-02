package headers

import (
	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// HeaderMap represents a collection of HTTP headers
type HeaderMap map[string]string

// New creates a middleware that sets HTTP headers on responses.
// Headers are set before the request is processed, allowing other middleware
// and handlers to override them if needed.
func New(headers HeaderMap) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		// Set headers before processing
		for key, value := range headers {
			rp.Writer().Header().Set(key, value)
		}

		// Continue processing
		rp.Next()
	}
}

// JSON creates a middleware that sets JSON-specific headers.
// Sets Content-Type to application/json and Cache-Control to no-cache.
func JSON() httpserver.HandlerFunc {
	return New(HeaderMap{
		"Content-Type":  "application/json",
		"Cache-Control": "no-cache",
	})
}

// CORS creates a middleware that sets CORS headers for cross-origin requests.
//
// Parameters:
//   - allowOrigin: Which origins can access the resource ("*" for any, or specific domain)
//   - allowMethods: Comma-separated list of allowed HTTP methods
//   - allowHeaders: Comma-separated list of allowed request headers
//
// Examples:
//
//	// Allow any origin (useful for public APIs)
//	CORS("*", "GET,POST,PUT,DELETE", "Content-Type,Authorization")
//
//	// Allow specific origin with credentials
//	CORS("https://app.example.com", "GET,POST", "Content-Type,Authorization")
//
//	// Minimal read-only API
//	CORS("*", "GET,OPTIONS", "Content-Type")
//
//	// Development setup with all methods
//	CORS("http://localhost:3000", "GET,POST,PUT,PATCH,DELETE,OPTIONS", "*")
func CORS(allowOrigin, allowMethods, allowHeaders string) httpserver.HandlerFunc {
	headers := HeaderMap{
		"Access-Control-Allow-Origin":  allowOrigin,
		"Access-Control-Allow-Methods": allowMethods,
		"Access-Control-Allow-Headers": allowHeaders,
	}

	// Add credentials header if origin is not wildcard
	if allowOrigin != "*" {
		headers["Access-Control-Allow-Credentials"] = "true"
	}

	return New(headers)
}

// Security creates a middleware that sets common security headers.
func Security() httpserver.HandlerFunc {
	return New(HeaderMap{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "1; mode=block",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	})
}

// Add creates a middleware that adds a single header.
// This is useful for simple header additions.
func Add(key, value string) httpserver.HandlerFunc {
	return New(HeaderMap{key: value})
}
