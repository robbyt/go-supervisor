package example

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWrapHandlerForJSON(t *testing.T) {
	t.Parallel()

	t.Run("transforms text response to JSON", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns plain text
		textHandler := func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("Hello World"))
			assert.NoError(t, err)
		}

		// Wrap handler with JSON enforcement
		wrappedHandler := WrapHandlerForJSON(textHandler)
		wrappedHandler(rec, req)

		assert.Contains(
			t,
			rec.Body.String(),
			`{"response":"Hello World"}`,
			"text should be wrapped in JSON",
		)
	})

	t.Run("preserves JSON response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns JSON
		jsonHandler := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"message": "Hello World", "status": "success"}`))
			assert.NoError(t, err)
		}

		wrappedHandler := WrapHandlerForJSON(jsonHandler)
		wrappedHandler(rec, req)

		assert.Equal(
			t,
			`{"message": "Hello World", "status": "success"}`,
			rec.Body.String(),
			"JSON should be preserved",
		)
	})

	t.Run("handles status codes correctly", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns error status
		errorHandler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, err := w.Write([]byte("Page not found"))
			assert.NoError(t, err)
		}

		wrappedHandler := WrapHandlerForJSON(errorHandler)
		wrappedHandler(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, "status code should be preserved")
		assert.Contains(
			t,
			rec.Body.String(),
			`{"response":"Page not found"}`,
			"error message should be wrapped",
		)
	})

	t.Run("handles empty response body", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that writes nothing
		emptyHandler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}

		wrappedHandler := WrapHandlerForJSON(emptyHandler)
		wrappedHandler(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code, "status code should be preserved")
		assert.Equal(
			t,
			`{"response":""}`+"\n",
			rec.Body.String(),
			"empty response should be wrapped",
		)
	})

	t.Run("transforms HTML response to JSON", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns HTML
		htmlHandler := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, err := w.Write([]byte("<html><body>Hello World</body></html>"))
			assert.NoError(t, err)
		}

		wrappedHandler := WrapHandlerForJSON(htmlHandler)
		wrappedHandler(rec, req)

		// JSON encoding will escape HTML characters
		assert.Contains(t, rec.Body.String(), `{"response":"`, "HTML should be wrapped in JSON")
		assert.Contains(t, rec.Body.String(), `html`, "should contain html content")
		assert.Contains(t, rec.Body.String(), `body`, "should contain body content")
	})

	t.Run("handles JSON array responses", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns JSON array
		arrayHandler := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`["item1", "item2", "item3"]`))
			assert.NoError(t, err)
		}

		wrappedHandler := WrapHandlerForJSON(arrayHandler)
		wrappedHandler(rec, req)

		assert.Equal(
			t,
			`["item1", "item2", "item3"]`,
			rec.Body.String(),
			"JSON array should be preserved",
		)
	})

	t.Run("handles malformed JSON as text", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns malformed JSON
		malformedHandler := func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"key": "value"`))
			assert.NoError(t, err)
		}

		wrappedHandler := WrapHandlerForJSON(malformedHandler)
		wrappedHandler(rec, req)

		assert.Contains(
			t,
			rec.Body.String(),
			`{"response":"{\"key\": \"value\""}`,
			"malformed JSON should be wrapped as text",
		)
	})

	t.Run("handles subsequent writes", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that makes multiple writes
		multiWriteHandler := func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("Hello"))
			assert.NoError(t, err)
			_, err = w.Write([]byte(" "))
			assert.NoError(t, err)
			_, err = w.Write([]byte("World"))
			assert.NoError(t, err)
		}

		wrappedHandler := WrapHandlerForJSON(multiWriteHandler)
		wrappedHandler(rec, req)

		body := rec.Body.String()
		assert.Equal(
			t,
			`{"response":"Hello World"}`+"\n",
			body,
			"all writes should be buffered and wrapped",
		)
	})
}
