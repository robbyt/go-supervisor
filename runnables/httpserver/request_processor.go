// This file defines the core middleware execution types.
//
// RequestProcessor manages the middleware chain execution and provides access
// to request/response data. It handles the control flow ("when" middleware runs)
// while ResponseWriter (response_writer.go) handles data capture ("what" data
// is available).
//
// When writing custom middleware, use RequestProcessor methods to:
// - Continue processing: rp.Next()
// - Stop processing: rp.Abort()
// - Access request: rp.Request()
// - Access response: rp.Writer()
package httpserver

import (
	"net/http"
)

// HandlerFunc is the middleware/handler signature
type HandlerFunc func(*RequestProcessor)

// RequestProcessor carries the request/response and middleware chain
type RequestProcessor struct {
	// Public fields for direct access
	writer  ResponseWriter
	request *http.Request

	// Private fields
	handlers []HandlerFunc
	index    int
}

// Next executes the remaining handlers in the chain
func (rp *RequestProcessor) Next() {
	rp.index++
	for rp.index < len(rp.handlers) {
		rp.handlers[rp.index](rp)
		rp.index++
	}
}

// Abort prevents remaining handlers from being called
func (rp *RequestProcessor) Abort() {
	rp.index = len(rp.handlers)
}

// IsAborted returns true if the request processing was aborted
func (rp *RequestProcessor) IsAborted() bool {
	return rp.index >= len(rp.handlers)
}

// Writer returns the ResponseWriter for the request
func (rp *RequestProcessor) Writer() ResponseWriter {
	return rp.writer
}

// Request returns the HTTP request
func (rp *RequestProcessor) Request() *http.Request {
	return rp.request
}

// SetWriter replaces the ResponseWriter for the request.
// This allows middleware to intercept and transform responses.
func (rp *RequestProcessor) SetWriter(w ResponseWriter) {
	rp.writer = w
}
