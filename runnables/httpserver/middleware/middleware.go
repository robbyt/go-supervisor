package middleware

import (
	"net/http"
)

// Middleware is a function that takes an http.HandlerFunc and returns a new http.HandlerFunc
// which may execute code before and/or after calling the original handler.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// responseWriter is a wrapper for http.ResponseWriter that captures the status code.
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

// WriteHeader captures the status code and calls the underlying WriteHeader.
func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.written = true
	rw.ResponseWriter.WriteHeader(statusCode)
}

// Write captures that a response has been written and calls the underlying Write.
func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.written = true
	}
	return rw.ResponseWriter.Write(b)
}
