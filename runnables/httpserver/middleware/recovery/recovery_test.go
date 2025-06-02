package recovery

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
)

// setupRequest creates a basic HTTP request for testing
func setupRequest(t *testing.T, method, path string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	return rec, req
}

// setupLogBuffer creates a handler that writes to a buffer for testing
func setupLogBuffer(t *testing.T, level slog.Level) (*bytes.Buffer, slog.Handler) {
	t.Helper()
	buffer := &bytes.Buffer{}
	handler := slog.NewTextHandler(buffer, &slog.HandlerOptions{Level: level})
	return buffer, handler
}

// executeHandlerWithRecovery runs the provided handler with the PanicRecovery middleware
func executeHandlerWithRecovery(
	t *testing.T,
	handler http.HandlerFunc,
	logHandler slog.Handler,
	rec *httptest.ResponseRecorder,
	req *http.Request,
) {
	t.Helper()
	// Create a route with recovery middleware and the handler
	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test", handler, New(logHandler))
	assert.NoError(t, err)
	route.ServeHTTP(rec, req)
}

// createPanicHandler returns a handler that panics with the given message
func createPanicHandler(t *testing.T, panicMsg string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		panic(panicMsg)
	}
}

// createSuccessHandler returns a handler that returns a 200 OK with "Success" body
func createSuccessHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		n, err := w.Write([]byte("Success"))
		assert.NoError(t, err)
		assert.Equal(t, 7, n)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	t.Run("recovers from panic with custom handler", func(t *testing.T) {
		// Setup
		logBuffer, logHandler := setupLogBuffer(t, slog.LevelError)
		rec, req := setupRequest(t, "GET", "/test")
		handler := createPanicHandler(t, "test panic")

		// Execute
		executeHandlerWithRecovery(t, handler, logHandler, rec, req)

		// Verify response
		resp := rec.Result()
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, "Internal Server Error\n", string(body))

		// Verify log output contains panic info
		logOutput := logBuffer.String()
		assert.Contains(t, logOutput, "HTTP handler panic recovered")
		assert.Contains(t, logOutput, "error=\"test panic\"")
		assert.Contains(t, logOutput, "path=/test")
		assert.Contains(t, logOutput, "method=GET")
	})

	t.Run("recovers from panic silently with nil handler", func(t *testing.T) {
		// Setup
		rec, req := setupRequest(t, "POST", "/api/test")
		handler := createPanicHandler(t, "test panic with nil handler")

		// Execute with nil handler - should recover silently
		executeHandlerWithRecovery(t, handler, nil, rec, req)

		// Verify response - should still return 500 error
		resp := rec.Result()
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		assert.Equal(t, "Internal Server Error\n", string(body))

		// No log verification since recovery should be silent
	})

	t.Run("passes through normal requests", func(t *testing.T) {
		// Setup
		rec, req := setupRequest(t, "GET", "/test")
		handler := createSuccessHandler(t)

		// Execute
		executeHandlerWithRecovery(t, handler, nil, rec, req)

		// Verify response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "Success", rec.Body.String())
	})
}
