package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// createStatusHandler returns a handler that returns a specific status code and message
func createStatusHandler(t *testing.T, status int, message string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Millisecond) // Ensure we get some duration
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
	metricsMiddleware := MetricCollector()
	wrappedHandler := metricsMiddleware(handler)
	wrappedHandler(rec, req)
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

	// This is a more direct test of the responseWriter in the metrics middleware
	t.Run("captures status code in responseWriter", func(t *testing.T) {
		// Setup direct responseWriter (without middleware)
		rec := httptest.NewRecorder()
		rw := &responseWriter{
			ResponseWriter: rec,
			statusCode:     http.StatusOK, // Default
		}

		// Execute direct writes
		rw.WriteHeader(http.StatusCreated)
		n, err := rw.Write([]byte("Created"))

		// Verify
		assert.NoError(t, err)
		assert.Equal(t, 7, n)
		assert.Equal(t, http.StatusCreated, rw.statusCode)
		assert.True(t, rw.written)
		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, "Created", rec.Body.String())
	})
}
