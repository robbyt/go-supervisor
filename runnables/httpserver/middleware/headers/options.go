package headers

import (
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// HeaderOperation represents a single header manipulation operation
type HeaderOperation func(*headerOperations)

type headerOperations struct {
	setHeaders           http.Header
	addHeaders           http.Header
	removeHeaders        []string
	setRequestHeaders    http.Header
	addRequestHeaders    http.Header
	removeRequestHeaders []string
}

// WithSet creates an operation to set (replace) headers
func WithSet(headers http.Header) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.setHeaders == nil {
			ops.setHeaders = make(http.Header)
		}
		for key, values := range headers {
			ops.setHeaders[key] = values
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
func WithAdd(headers http.Header) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.addHeaders == nil {
			ops.addHeaders = make(http.Header)
		}
		for key, values := range headers {
			for _, value := range values {
				ops.addHeaders.Add(key, value)
			}
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

// WithSetRequest creates an operation to set (replace) request headers
func WithSetRequest(headers http.Header) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.setRequestHeaders == nil {
			ops.setRequestHeaders = make(http.Header)
		}
		for key, values := range headers {
			ops.setRequestHeaders[key] = values
		}
	}
}

// WithSetRequestHeader creates an operation to set a single request header
func WithSetRequestHeader(key, value string) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.setRequestHeaders == nil {
			ops.setRequestHeaders = make(http.Header)
		}
		ops.setRequestHeaders.Set(key, value)
	}
}

// WithAddRequest creates an operation to add (append) request headers
func WithAddRequest(headers http.Header) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.addRequestHeaders == nil {
			ops.addRequestHeaders = make(http.Header)
		}
		for key, values := range headers {
			for _, value := range values {
				ops.addRequestHeaders.Add(key, value)
			}
		}
	}
}

// WithAddRequestHeader creates an operation to add a single request header
func WithAddRequestHeader(key, value string) HeaderOperation {
	return func(ops *headerOperations) {
		if ops.addRequestHeaders == nil {
			ops.addRequestHeaders = make(http.Header)
		}
		ops.addRequestHeaders.Add(key, value)
	}
}

// WithRemoveRequest creates an operation to remove request headers
func WithRemoveRequest(headerNames ...string) HeaderOperation {
	return func(ops *headerOperations) {
		ops.removeRequestHeaders = append(ops.removeRequestHeaders, headerNames...)
	}
}

// NewWithOperations creates a middleware with full header control using functional options.
// Operations are executed in order: remove → set → add (for both request and response headers)
func NewWithOperations(operations ...HeaderOperation) httpserver.HandlerFunc {
	ops := &headerOperations{}
	for _, operation := range operations {
		operation(ops)
	}

	return func(rp *httpserver.RequestProcessor) {
		request := rp.Request()
		writer := rp.Writer()

		// Request header manipulation (before calling Next)
		// 1. Remove request headers first
		for _, key := range ops.removeRequestHeaders {
			request.Header.Del(key)
		}

		// 2. Set request headers (replace)
		for key, values := range ops.setRequestHeaders {
			request.Header.Del(key)
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}

		// 3. Add request headers (append)
		for key, values := range ops.addRequestHeaders {
			for _, value := range values {
				request.Header.Add(key, value)
			}
		}

		// Response header manipulation (existing functionality)
		// 1. Remove response headers first
		for _, key := range ops.removeHeaders {
			writer.Header().Del(key)
		}

		// 2. Set response headers (replace)
		for key, values := range ops.setHeaders {
			writer.Header().Del(key)
			for _, value := range values {
				writer.Header().Add(key, value)
			}
		}

		// 3. Add response headers (append)
		for key, values := range ops.addHeaders {
			for _, value := range values {
				writer.Header().Add(key, value)
			}
		}

		rp.Next()
	}
}
