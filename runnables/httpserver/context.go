package httpserver

import (
	"net/http"
)

// HandlerFunc is the new middleware/handler signature
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
