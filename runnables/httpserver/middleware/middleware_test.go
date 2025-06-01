package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseWriter(t *testing.T) {
	t.Run("WriteHeader sets status code", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
			statusCode:     http.StatusOK, // Default
		}

		// Write a custom status code
		rw.WriteHeader(http.StatusNotFound)

		// Check that the status code was set in our wrapper
		assert.Equal(t, http.StatusNotFound, rw.statusCode)
		// Check that the status code was passed to the underlying ResponseWriter
		assert.Equal(t, http.StatusNotFound, rec.Code)
		// Check that written flag was set
		assert.True(t, rw.written)
	})

	t.Run("Write sets written flag", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
			statusCode:     http.StatusOK,
			written:        false,
		}

		// Write some content
		n, err := rw.Write([]byte("test"))
		require.NoError(t, err)
		require.Equal(t, 4, n)

		// Check written flag is set
		assert.True(t, rw.written)
		// Status code should remain default
		assert.Equal(t, http.StatusOK, rw.statusCode)
		// Content should be written to underlying ResponseWriter
		assert.Equal(t, "test", rec.Body.String())
	})

	t.Run("Write doesn't change written flag if already set", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
			statusCode:     http.StatusOK,
			written:        true, // Already set
		}

		// Write some content
		_, err := rw.Write([]byte("test"))
		require.NoError(t, err)

		// Check written flag is still true
		assert.True(t, rw.written)
	})

	t.Run("Integration with middleware", func(t *testing.T) {
		// Create a test middleware that uses the responseWriter
		testMiddleware := func(next http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				rw := &ResponseWriter{
					ResponseWriter: w,
					statusCode:     http.StatusOK,
				}
				next(rw, r)
				// We can now access the statusCode that was set by the handler
				assert.Equal(t, http.StatusCreated, rw.statusCode)
			}
		}

		// Create a handler that sets a status
		handler := func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			n, err := w.Write([]byte("Created"))
			assert.NoError(t, err)
			assert.Equal(t, 7, n)
		}

		// Create a test request and recorder
		req := httptest.NewRequest("POST", "/test", nil)
		rec := httptest.NewRecorder()

		// Apply middleware and execute
		wrappedHandler := testMiddleware(handler)
		wrappedHandler(rec, req)

		// Verify response
		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, "Created", rec.Body.String())
	})

	t.Run("Status() returns correct status", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
			statusCode:     http.StatusOK,
		}

		// No WriteHeader called yet
		assert.Equal(t, http.StatusOK, rw.Status())

		rw.WriteHeader(http.StatusTeapot)
		assert.Equal(t, http.StatusTeapot, rw.Status())
	})

	t.Run("BytesWritten() returns correct count", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
			statusCode:     http.StatusOK,
		}

		// No bytes written yet
		assert.Equal(t, 0, rw.BytesWritten())

		_, err := rw.Write([]byte("foo"))
		assert.NoError(t, err)
		_, err = rw.Write([]byte("barbaz"))
		assert.NoError(t, err)
		assert.Equal(t, 9, rw.BytesWritten())
	})

	t.Run("Status() and BytesWritten() default values", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
		}
		assert.Equal(t, 0, rw.Status())
		assert.Equal(t, 0, rw.BytesWritten())
	})
}
