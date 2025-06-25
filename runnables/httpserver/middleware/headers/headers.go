package headers

import (
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// New creates a middleware that sets HTTP headers on responses.
// Headers are set before the request is processed, allowing other middleware
// and handlers to override them if needed.
//
// Note: The Go standard library's http package will validate headers when
// writing them to prevent protocol violations. This middleware does not
// perform additional validation beyond what the standard library provides.
func New(headers http.Header) httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		// Set headers before processing
		for key, values := range headers {
			for _, value := range values {
				rp.Writer().Header().Add(key, value)
			}
		}

		// Continue processing
		rp.Next()
	}
}

// JSON creates a middleware that sets JSON-specific headers.
// Sets Content-Type to application/json and Cache-Control to no-cache.
func JSON() httpserver.HandlerFunc {
	return New(http.Header{
		"Content-Type":  []string{"application/json"},
		"Cache-Control": []string{"no-cache"},
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
	corsHeaders := http.Header{
		"Access-Control-Allow-Origin":  []string{allowOrigin},
		"Access-Control-Allow-Methods": []string{allowMethods},
		"Access-Control-Allow-Headers": []string{allowHeaders},
	}

	// Add credentials header if origin is not wildcard
	if allowOrigin != "*" {
		corsHeaders["Access-Control-Allow-Credentials"] = []string{"true"}
	}

	return NewWithOperations(WithSet(corsHeaders))
}

// Security creates a middleware that sets common security headers.
func Security() httpserver.HandlerFunc {
	return New(http.Header{
		"X-Content-Type-Options": []string{"nosniff"},
		"X-Frame-Options":        []string{"DENY"},
		"X-XSS-Protection":       []string{"1; mode=block"},
		"Referrer-Policy":        []string{"strict-origin-when-cross-origin"},
	})
}

// Add creates a middleware that adds a single header.
// This is useful for simple header additions.
func Add(key, value string) httpserver.HandlerFunc {
	return New(http.Header{key: []string{value}})
}
