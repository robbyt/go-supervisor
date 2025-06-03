package example

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// ResponseWrapper captures response data for transformation
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
	// First check if the data is valid JSON regardless of content type
	var test any
	if json.Unmarshal(data, &test) == nil {
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

		// Copy headers to original writer
		originalWriter := wrapper.original
		for key, values := range wrapper.Header() {
			for _, value := range values {
				originalWriter.Header().Add(key, value)
			}
		}

		// Ensure JSON content type
		originalWriter.Header().Set("Content-Type", "application/json")

		// Write status and transformed data
		statusCode := wrapper.Status()
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		originalWriter.WriteHeader(statusCode)
		if _, err := originalWriter.Write(jsonData); err != nil {
			// Response is already committed, cannot recover from write error
			return
		}
	}
}
