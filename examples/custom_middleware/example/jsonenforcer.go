package example

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// ResponseWrapper captures response data for transformation
//
// This is a simple example for demonstration purposes and is not intended for
// production use. Limitations:
// - Does not preserve optional HTTP interfaces (http.Hijacker, http.Flusher, http.Pusher)
// - Not safe for concurrent writes from multiple goroutines within the same request
// - No memory limits on buffered content
//
// Each request gets its own ResponseWrapper instance, so different requests won't
// interfere with each other.
type ResponseWrapper struct {
	original httpserver.ResponseWriter
	buffer   *bytes.Buffer
	headers  http.Header
	status   int
}

// NewResponseWrapper creates a new response wrapper
func NewResponseWrapper(original httpserver.ResponseWriter) *ResponseWrapper {
	return &ResponseWrapper{
		original: original,
		buffer:   new(bytes.Buffer),
		headers:  make(http.Header),
		status:   0, // 0 means not set yet
	}
}

// Header implements http.ResponseWriter
func (rw *ResponseWrapper) Header() http.Header {
	return rw.headers
}

// Write implements http.ResponseWriter
func (rw *ResponseWrapper) Write(data []byte) (int, error) {
	return rw.buffer.Write(data)
}

// WriteHeader implements http.ResponseWriter
func (rw *ResponseWrapper) WriteHeader(statusCode int) {
	if rw.status == 0 {
		rw.status = statusCode
	}
}

// Status implements httpserver.ResponseWriter
func (rw *ResponseWrapper) Status() int {
	if rw.status == 0 && rw.buffer.Len() > 0 {
		return http.StatusOK
	}
	return rw.status
}

// Written implements httpserver.ResponseWriter
func (rw *ResponseWrapper) Written() bool {
	return rw.buffer.Len() > 0 || rw.status != 0
}

// Size implements httpserver.ResponseWriter
func (rw *ResponseWrapper) Size() int {
	return rw.buffer.Len()
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
		// Wrap the response writer to capture output
		wrapper := NewResponseWrapper(rp.Writer())
		rp.SetWriter(wrapper)

		// Continue to next middleware/handler
		rp.Next()

		// RESPONSE PHASE: Transform response to JSON
		originalData := wrapper.buffer.Bytes()
		statusCode := wrapper.Status()
		if statusCode == 0 {
			statusCode = http.StatusOK
		}

		// Copy headers to original writer
		originalWriter := wrapper.original
		for key, values := range wrapper.Header() {
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
		if len(originalData) == 0 && wrapper.status == 0 {
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
