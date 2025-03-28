package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
	wrappedHandler := StateDebugger(stateProvider)(handler)
	wrappedHandler(rec, req)
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
