package headers

import (
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// HeaderOperation represents a single header manipulation operation
type HeaderOperation func(*headerOperations)

type headerOperations struct {
	setHeaders    http.Header
	addHeaders    http.Header
	removeHeaders []string
}

// WithSet creates an operation to set (replace) headers
func WithSet(headers HeaderMap) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.setHeaders == nil {
			ops.setHeaders = make(http.Header)
		}
		for key, value := range headers {
			ops.setHeaders.Set(key, value)
		}
	}
}

// WithSetHeader creates an operation to set a single header
func WithSetHeader(key, value string) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.setHeaders == nil {
			ops.setHeaders = make(http.Header)
		}
		ops.setHeaders.Set(key, value)
	}
}

// WithAdd creates an operation to add (append) headers
func WithAdd(headers HeaderMap) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.addHeaders == nil {
			ops.addHeaders = make(http.Header)
		}
		for key, value := range headers {
			ops.addHeaders.Add(key, value)
		}
	}
}

// WithAddHeader creates an operation to add a single header
func WithAddHeader(key, value string) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.addHeaders == nil {
			ops.addHeaders = make(http.Header)
		}
		ops.addHeaders.Add(key, value)
	}
}

// WithRemove creates an operation to remove headers
func WithRemove(headerNames ...string) HeaderOperation {
	return func(ops *headerOperations) {
		ops.removeHeaders = append(ops.removeHeaders, headerNames...)
	}
}

// NewWithOperations creates a middleware with full header control using functional options.
// Operations are executed in order: remove → set → add
func NewWithOperations(operations ...HeaderOperation) httpserver.HandlerFunc {
	ops := &headerOperations{}
	for _, operation := range operations {
		operation(ops)
	}

	return func(rp *httpserver.RequestProcessor) {
		writer := rp.Writer()

		// 1. Remove headers first
		for _, key := range ops.removeHeaders {
			writer.Header().Del(key)
		}

		// 2. Set headers (replace)
		for key, values := range ops.setHeaders {
			if len(values) > 0 {
				writer.Header().Set(key, values[0])
			}
		}

		// 3. Add headers (append)
		for key, values := range ops.addHeaders {
			for _, value := range values {
				writer.Header().Add(key, value)
			}
		}

		rp.Next()
	}
}
