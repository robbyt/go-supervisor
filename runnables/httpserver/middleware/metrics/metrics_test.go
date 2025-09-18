package metrics

import (
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

// createStatusHandler returns a handler that returns a specific status code and message
func createStatusHandler(t *testing.T, status int, message string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		n, err := w.Write([]byte(message))
		assert.NoError(t, err)
		assert.Equal(t, len(message), n)
	}
}

// executeHandlerWithMetrics runs the provided handler with the MetricCollector middleware
func executeHandlerWithMetrics(
	t *testing.T,
	handler http.HandlerFunc,
	rec *httptest.ResponseRecorder,
	req *http.Request,
) {
	t.Helper()
	// Create a route with metrics middleware and the handler
	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test", handler, New())
	require.NoError(t, err)
	route.ServeHTTP(rec, req)
}

func TestMetricCollector(t *testing.T) {
	t.Run("handles successful requests", func(t *testing.T) {
		// Setup
		rec, req := setupRequest(t, "GET", "/test")
		handler := createStatusHandler(t, http.StatusOK, "OK")

		// Execute
		executeHandlerWithMetrics(t, handler, rec, req)

		// Verify
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "OK", rec.Body.String())
	})

	t.Run("handles error requests", func(t *testing.T) {
		// Setup
		rec, req := setupRequest(t, "POST", "/error")
		handler := createStatusHandler(t, http.StatusInternalServerError, "Error")

		// Execute
		executeHandlerWithMetrics(t, handler, rec, req)

		// Verify
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, "Error", rec.Body.String())
	})
}
