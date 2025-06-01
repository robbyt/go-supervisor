package middleware

import (
	"errors"
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

		assert.Equal(t, http.StatusNotFound, rw.statusCode, "status code should be set in wrapper")
		assert.Equal(t, http.StatusNotFound, rec.Code, "status code should be passed to underlying ResponseWriter")
		assert.True(t, rw.written, "written flag should be set")
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

		assert.True(t, rw.written, "written flag should be set")
		assert.Equal(t, http.StatusOK, rw.statusCode, "status code should remain default")
		assert.Equal(t, "test", rec.Body.String(), "content should be written to underlying ResponseWriter")
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

		assert.True(t, rw.written, "written flag should still be true")
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
				assert.Equal(t, http.StatusCreated, rw.statusCode, "middleware should capture status code set by handler")
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

		assert.Equal(t, http.StatusOK, rw.Status(), "should return initial status code when no WriteHeader called")

		rw.WriteHeader(http.StatusTeapot)
		assert.Equal(t, http.StatusTeapot, rw.Status())
	})

	t.Run("BytesWritten() returns correct count", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
			statusCode:     http.StatusOK,
		}

		assert.Equal(t, 0, rw.BytesWritten(), "should return 0 bytes when nothing written")

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

	t.Run("Write() method error handling", func(t *testing.T) {
		// Create a mock ResponseWriter that returns an error
		mockWriter := &errorResponseWriter{err: errors.New("write failed")}
		rw := &ResponseWriter{
			ResponseWriter: mockWriter,
		}

		n, err := rw.Write([]byte("test"))
		assert.Error(t, err)
		assert.Equal(t, "write failed", err.Error())
		assert.Equal(t, 0, n)
		assert.Equal(t, 0, rw.BytesWritten(), "no bytes should be counted on error")
		assert.True(t, rw.written, "written flag should still be set")
	})

	t.Run("Status() without explicit WriteHeader()", func(t *testing.T) {
		rec := httptest.NewRecorder()
		rw := &ResponseWriter{
			ResponseWriter: rec,
		}

		// Call Write() without WriteHeader() - should keep default status (0)
		_, err := rw.Write([]byte("test"))
		assert.NoError(t, err)
		assert.Equal(t, 0, rw.Status(), "should remain 0 (default) when WriteHeader not called")
		assert.Equal(t, 4, rw.BytesWritten(), "should count bytes written")
		assert.True(t, rw.written, "should set written flag")
	})

	t.Run("Byte counting with partial writes", func(t *testing.T) {
		// Create a mock that only writes part of the data
		mockWriter := &partialResponseWriter{written: 2}
		rw := &ResponseWriter{
			ResponseWriter: mockWriter,
		}

		n, err := rw.Write([]byte("test"))
		assert.NoError(t, err)
		assert.Equal(t, 2, n, "only 2 bytes should be written by partial writer")
		assert.Equal(t, 2, rw.BytesWritten(), "should track actual bytes written")
		assert.True(t, rw.written)

		// Write again
		n, err = rw.Write([]byte("more"))
		assert.NoError(t, err)
		assert.Equal(t, 2, n, "only 2 bytes should be written again by partial writer")
		assert.Equal(t, 4, rw.BytesWritten(), "should accumulate bytes: 2 + 2 = 4")
	})
}

// errorResponseWriter is a mock that always returns an error on Write
type errorResponseWriter struct {
	err error
}

func (e *errorResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (e *errorResponseWriter) Write([]byte) (int, error) {
	return 0, e.err
}

func (e *errorResponseWriter) WriteHeader(int) {}

// partialResponseWriter is a mock that only writes a fixed number of bytes
type partialResponseWriter struct {
	written int
}

func (p *partialResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (p *partialResponseWriter) Write(data []byte) (int, error) {
	if len(data) < p.written {
		return len(data), nil
	}
	return p.written, nil
}

func (p *partialResponseWriter) WriteHeader(int) {}
