// Package httpserver provides HTTP server functionality with middleware support.
//
// This file defines ResponseWriter, which wraps the standard http.ResponseWriter
// to capture response metadata that the standard interface doesn't expose.
//
// # Why This Wrapper Exists
//
// The standard http.ResponseWriter doesn't provide access to the status code
// or byte count after they've been written. Middleware needs this information
// for logging, metrics, and conditional processing.
//
// # Relationship to Middleware
//
// Middleware functions receive a RequestProcessor that contains this ResponseWriter.
// The wrapper allows middleware to inspect response state after handlers execute.
//
// # Relationship to RequestProcessor (context.go)
//
// RequestProcessor manages middleware execution flow and provides access to this
// ResponseWriter through its Writer() method. The RequestProcessor handles the
// "when" of middleware execution, while ResponseWriter handles the "what" of
// response data capture.
package httpserver

import "net/http"

// ResponseWriter wraps http.ResponseWriter with additional functionality
type ResponseWriter interface {
	http.ResponseWriter

	// Status returns the HTTP status code
	Status() int

	// Written returns true if the response has been written
	Written() bool

	// Size returns the number of bytes written
	Size() int
}

type responseWriter struct {
	http.ResponseWriter
	status  int
	written bool
	size    int
}

// newResponseWriter creates a new ResponseWriter wrapper
func newResponseWriter(w http.ResponseWriter) ResponseWriter {
	return &responseWriter{
		ResponseWriter: w,
		status:         0,
		written:        false,
		size:           0,
	}
}

// WriteHeader captures the status code and calls the underlying WriteHeader
func (rw *responseWriter) WriteHeader(statusCode int) {
	if !rw.written {
		rw.status = statusCode
		rw.written = true
		rw.ResponseWriter.WriteHeader(statusCode)
	}
}

// Write captures that a response has been written, counts the bytes, and calls the underlying Write
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		// The status will be StatusOK if WriteHeader has not been called yet
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	return n, err
}

// Status returns the HTTP status code that was written to the response
func (rw *responseWriter) Status() int {
	if rw.status == 0 && rw.written {
		// If no explicit status was set but data was written, it's 200
		return http.StatusOK
	}
	return rw.status
}

// Written returns true if the response has been written
func (rw *responseWriter) Written() bool {
	return rw.written
}

// Size returns the number of bytes written to the response body
func (rw *responseWriter) Size() int {
	return rw.size
}
