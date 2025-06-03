package example

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
)

func TestJSONEnforcerMiddleware(t *testing.T) {
	t.Parallel()

	t.Run("transforms text response to JSON", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns plain text
		textHandler := func(w http.ResponseWriter, r *http.Request) {
			_, err := w.Write([]byte("Hello World"))
			assert.NoError(t, err)
		}

		// Create route with JSON enforcer middleware
		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			textHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
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

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			jsonHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
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

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			errorHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code, "status code should be preserved")
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		assert.Contains(
			t,
			rec.Body.String(),
			`{"response":"Page not found"}`,
			"error message should be wrapped",
		)
	})

	t.Run("handles 204 No Content correctly", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns 204 No Content
		emptyHandler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			emptyHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNoContent, rec.Code, "status code should be preserved")
		assert.Empty(t, rec.Body.String(), "204 responses must have no body per HTTP spec")
		assert.Equal(t, 0, rec.Body.Len(), "content length should be 0")
	})

	t.Run("handles 304 Not Modified correctly", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Handler that returns 304 Not Modified
		notModifiedHandler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotModified)
			// Even if handler writes, middleware should not include body
			_, err := w.Write([]byte("should be ignored"))
			assert.NoError(t, err)
		}

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			notModifiedHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotModified, rec.Code, "status code should be preserved")
		assert.Empty(t, rec.Body.String(), "304 responses must have no body per HTTP spec")
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

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			htmlHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
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

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			arrayHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
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

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			malformedHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
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

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			multiWriteHandler,
			New(),
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		body := rec.Body.String()
		assert.Equal(
			t,
			`{"response":"Hello World"}`,
			body,
			"all writes should be buffered and wrapped",
		)
	})
}

// Test compliance with the middleware framework
func TestJSONEnforcerCompliance(t *testing.T) {
	// This is a placeholder test that would import the compliance framework
	// In practice, you would add this to runnables/httpserver/middleware/compliance_test.go
	jsonMiddleware := New()

	// Test basic compliance: calls Next()
	t.Run("calls Next()", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		called := false
		mockHandler := func(w http.ResponseWriter, r *http.Request) {
			called = true
		}

		route, err := httpserver.NewRouteFromHandlerFunc(
			"test",
			"/test",
			mockHandler,
			jsonMiddleware,
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)
		assert.True(t, called, "should call the next handler")
	})
}
