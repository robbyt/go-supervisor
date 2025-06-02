package state

import (
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

// createStateProvider returns a function that provides a static state string
func createStateProvider(t *testing.T, state string) func() string {
	t.Helper()
	return func() string {
		return state
	}
}

// executeHandlerWithState runs the provided handler with the StateDebugger middleware
func executeHandlerWithState(
	t *testing.T,
	handler http.HandlerFunc,
	stateProvider func() string,
	rec *httptest.ResponseRecorder,
	req *http.Request,
) {
	t.Helper()
	// Create a route with state middleware and the handler
	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test", handler, New(stateProvider))
	assert.NoError(t, err)
	route.ServeHTTP(rec, req)
}

func TestStateMiddleware(t *testing.T) {
	// Setup
	rec, req := setupRequest(t, "GET", "/test")
	handler := createTestHandler(t, false) // Reusing helper from logger_test.go
	stateProvider := createStateProvider(t, "running")

	// Execute
	executeHandlerWithState(t, handler, stateProvider, rec, req)

	// Verify
	resp := rec.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "running", resp.Header.Get("X-Server-State"))
}
