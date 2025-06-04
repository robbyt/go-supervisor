package example

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// ResponseBuffer captures response data for transformation
//
// This is a simple example for demonstration purposes and is not intended for
// production use. Limitations:
// - Does not preserve optional HTTP interfaces (http.Hijacker, http.Flusher, http.Pusher)
// - Not safe for concurrent writes from multiple goroutines within the same request
// - No memory limits on buffered content
//
// Each request gets its own ResponseBuffer instance, so different requests won't
// interfere with each other.
type ResponseBuffer struct {
	buffer  *bytes.Buffer
	headers http.Header
	status  int
}

// NewResponseBuffer creates a new response buffer
func NewResponseBuffer() *ResponseBuffer {
	return &ResponseBuffer{
		buffer:  new(bytes.Buffer),
		headers: make(http.Header),
		status:  0, // 0 means not set yet
	}
}

// Header implements http.ResponseWriter
func (rb *ResponseBuffer) Header() http.Header {
	return rb.headers
}

// Write implements http.ResponseWriter
func (rb *ResponseBuffer) Write(data []byte) (int, error) {
	return rb.buffer.Write(data)
}

// WriteHeader implements http.ResponseWriter
func (rb *ResponseBuffer) WriteHeader(statusCode int) {
	if rb.status == 0 {
		rb.status = statusCode
	}
}

// Status implements httpserver.ResponseWriter
func (rb *ResponseBuffer) Status() int {
	if rb.status == 0 && rb.buffer.Len() > 0 {
		return http.StatusOK
	}
	return rb.status
}

// Written implements httpserver.ResponseWriter
func (rb *ResponseBuffer) Written() bool {
	return rb.buffer.Len() > 0 || rb.status != 0
}

// Size implements httpserver.ResponseWriter
func (rb *ResponseBuffer) Size() int {
	return rb.buffer.Len()
}

// transformToJSON wraps non-JSON content in a JSON response
func transformToJSON(data []byte) ([]byte, error) {
	// Use json.Valid for efficient validation without unmarshaling
	if json.Valid(data) {
		return data, nil // Valid JSON, return as-is
	}

	// If not valid JSON, wrap it
	response := map[string]string{
		"response": string(data),
	}

	return json.Marshal(response)
}

// New creates a middleware that transforms all responses to JSON format.
// Non-JSON responses are wrapped in {"response": "content"}.
// Valid JSON responses are preserved as-is.
func New() httpserver.HandlerFunc {
	return func(rp *httpserver.RequestProcessor) {
		// Store original writer before buffering
		originalWriter := rp.Writer()

		// Buffer the response to capture output
		buffer := NewResponseBuffer()
		rp.SetWriter(buffer)

		// Continue to next middleware/handler
		rp.Next()

		// RESPONSE PHASE: Transform response to JSON
		originalData := buffer.buffer.Bytes()
		statusCode := buffer.Status()
		if statusCode == 0 {
			statusCode = http.StatusOK
		}

		// Copy headers to original writer
		for key, values := range buffer.Header() {
			for _, value := range values {
				originalWriter.Header().Add(key, value)
			}
		}

		// Check if this status code should have no body per HTTP spec
		// 204 No Content and 304 Not Modified MUST NOT have a message body
		if statusCode == http.StatusNoContent || statusCode == http.StatusNotModified {
			originalWriter.WriteHeader(statusCode)
			return
		}

		// Transform captured data to JSON
		if len(originalData) == 0 && buffer.status == 0 {
			return
		}

		// Transform to JSON if needed
		jsonData, err := transformToJSON(originalData)
		if err != nil {
			// Fallback: wrap error in JSON
			jsonData = []byte(`{"error":"Unable to encode response"}`)
		}

		// Ensure JSON content type
		originalWriter.Header().Set("Content-Type", "application/json")

		// Write status and transformed data
		originalWriter.WriteHeader(statusCode)
		if _, err := originalWriter.Write(jsonData); err != nil {
			// Response is already committed, cannot recover from write error
			return
		}
	}
}
