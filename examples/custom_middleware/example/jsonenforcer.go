package example

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// jsonResponseWriter captures and transforms responses to JSON
type jsonResponseWriter struct {
	http.ResponseWriter
	buffer     bytes.Buffer
	statusCode int
}

func (w *jsonResponseWriter) WriteHeader(code int) {
	w.statusCode = code
}

func (w *jsonResponseWriter) Write(data []byte) (int, error) {
	return w.buffer.Write(data)
}

// WrapHandlerForJSON wraps an http.HandlerFunc to ensure all responses are JSON.
//
// This wrapper intercepts the response by using a custom ResponseWriter that buffers
// all writes. After the handler completes, it examines the buffered content:
// - If the content is already valid JSON, it's written as-is
// - If not, it's wrapped in a JSON object: {"response": "original content"}
//
// Header Handling:
// This wrapper does not set any headers - header management is handled by dedicated
// header middleware (such as the headers middleware package). This maintains clean
// separation of concerns where this middleware only handles response body transformation.
//
// Status Code Handling:
// The explicit w.WriteHeader() call is required because:
// 1. Our custom writer captures the status code but doesn't send it
// 2. We need to send the captured status code to the real ResponseWriter
// 3. If WriteHeader isn't called explicitly, Go defaults to 200 OK
//
// Middleware Interaction:
// This wrapper works correctly with other middleware because:
// - It runs at the handler level (after all middleware has processed)
// - Headers set by earlier middleware are preserved and sent normally
// - Only the response body content is transformed, not headers or status codes
func WrapHandlerForJSON(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Create custom writer that buffers the response
		jw := &jsonResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Call the original handler
		handler(jw, r)

		w.WriteHeader(jw.statusCode)

		// Get the buffered response
		content := jw.buffer.Bytes()

		// If it's valid JSON, write it as-is
		if json.Valid(content) {
			if _, err := w.Write(content); err != nil {
				http.Error(w, "Failed to write response", http.StatusInternalServerError)
			}
			return
		}

		// Otherwise, wrap it in a JSON object
		response := map[string]string{"response": string(content)}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, "Failed to encode JSON", http.StatusInternalServerError)
		}
	}
}
