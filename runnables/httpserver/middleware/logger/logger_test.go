package logger

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// createTestHandler returns a handler that writes a response
func createTestHandler(t *testing.T, checkResponse bool) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		n, err := w.Write([]byte("test response"))
		if checkResponse {
			assert.NoError(t, err)
			assert.Equal(t, 13, n)
		} else if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
			return
		}
	}
}

// executeHandlerWithLogger runs the provided handler with the Logger middleware
func executeHandlerWithLogger(
	t *testing.T,
	handler http.HandlerFunc,
	logHandler slog.Handler,
	rec *httptest.ResponseRecorder,
	req *http.Request,
) {
	t.Helper()
	// Create a route with logger middleware and the handler
	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test", handler, New(logHandler))
	require.NoError(t, err)
	route.ServeHTTP(rec, req)
}

// setupDetailedRequest creates a test HTTP request with user agent and remote addr
func setupDetailedRequest(
	t *testing.T,
	method, path, userAgent, remoteAddr string,
) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	rec, req := setupRequest(t, method, path)
	req.Header.Set("User-Agent", userAgent)
	req.RemoteAddr = remoteAddr
	return rec, req
}

func TestLogger(t *testing.T) {
	t.Run("with custom handler", func(t *testing.T) {
		// Setup
		logBuffer, logHandler := setupLogBuffer(t, slog.LevelInfo)
		rec, req := setupDetailedRequest(t, "GET", "/test", "test-agent", "127.0.0.1:12345")
		handler := createTestHandler(t, false)

		// Execute
		executeHandlerWithLogger(t, handler, logHandler, rec, req)

		// Check response
		resp := rec.Result()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "test response", string(body))

		// Verify log output contains expected info
		logOutput := logBuffer.String()
		assert.Contains(t, logOutput, "HTTP request")
		assert.Contains(t, logOutput, "method=GET")
		assert.Contains(t, logOutput, "path=/test")
		assert.Contains(t, logOutput, "status=200")
		assert.Contains(t, logOutput, "user_agent=test-agent")
		assert.Contains(t, logOutput, "remote_addr=127.0.0.1:12345")
	})

	t.Run("with nil handler (uses default)", func(t *testing.T) {
		// Save and restore default logger
		defaultLogger := slog.Default()
		defer slog.SetDefault(defaultLogger)

		// Setup with default logger
		logBuffer, testHandler := setupLogBuffer(t, slog.LevelInfo)
		slog.SetDefault(slog.New(testHandler))

		rec, req := setupRequest(t, "GET", "/test")
		handler := createTestHandler(t, true)

		// Execute with nil handler (will use default)
		executeHandlerWithLogger(t, handler, nil, rec, req)

		// Verify log output
		logOutput := logBuffer.String()
		assert.Contains(t, logOutput, "httpserver")
		assert.Contains(t, logOutput, "HTTP request")
	})
}
