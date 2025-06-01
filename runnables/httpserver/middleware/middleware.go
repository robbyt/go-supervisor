// Package middleware provides HTTP middleware utilities for wrapping http.HandlerFunc with additional functionality such as logging, metrics, and response inspection.
package middleware

import (
	"net/http"
)

// Middleware is a function that takes an http.HandlerFunc and returns a new http.HandlerFunc
// which may execute code before and/or after calling the original handler. It is commonly used
// for cross-cutting concerns such as logging, authentication, and metrics.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// ResponseWriter is a wrapper for http.ResponseWriter that captures the status code and the number of bytes written.
// It is useful in middleware for logging, metrics, and conditional logic based on the response.
type ResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	written      bool
	bytesWritten int
}

// WriteHeader captures the status code and calls the underlying WriteHeader.
func (rw *ResponseWriter) WriteHeader(statusCode int) {
	rw.written = true
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

// Write captures that a response has been written, counts the bytes, and calls the underlying Write.
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	rw.written = true
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

// Status returns the HTTP status code that was written to the response.
// If no status code was explicitly set, it returns 0.
func (rw *ResponseWriter) Status() int {
	return rw.statusCode
}

// BytesWritten returns the total number of bytes written to the response body.
func (rw *ResponseWriter) BytesWritten() int {
	return rw.bytesWritten
}
