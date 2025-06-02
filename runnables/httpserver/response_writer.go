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
