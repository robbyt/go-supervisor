package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHeadersMiddlewareExample tests the headers middleware functionality
func TestHeadersMiddlewareExample(t *testing.T) {
	t.Parallel()

	// Create a test logger that discards output
	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})

	// Create a context with timeout for the test
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Build routes
	routes, err := buildRoutes(logHandler)
	require.NoError(t, err, "Failed to build routes")
	require.NotEmpty(t, routes, "Routes should not be empty")

	// Create the HTTP server
	httpServer, err := createHTTPServer(routes, logHandler)
	require.NoError(t, err, "Failed to create HTTP server")
	require.NotNil(t, httpServer, "HTTP server should not be nil")

	// Create the supervisor
	sv, err := createSupervisor(ctx, logHandler, httpServer)
	require.NoError(t, err, "Failed to create supervisor")
	require.NotNil(t, sv, "Supervisor should not be nil")

	// Start the server in a goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- sv.Run()
	}()

	// Give the server time to start
	time.Sleep(200 * time.Millisecond)

	t.Run("TestSimpleRoute", func(t *testing.T) {
		// Test simple route
		resp, err := http.Get("http://localhost:8082/")
		require.NoError(t, err, "Failed to make GET request")
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()

		// Check response headers are set by middleware
		assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
		assert.Equal(t, "v1.0", resp.Header.Get("X-API-Version"))
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		assert.Equal(t, "go-supervisor-headers", resp.Header.Get("X-Custom-Header"))
		assert.NotEmpty(t, resp.Header.Get("X-Response-Time"))

		// Check that server identification headers are removed
		assert.Empty(t, resp.Header.Get("Server"))
		assert.Empty(t, resp.Header.Get("X-Powered-By"))

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("TestHeaderInspectionRoute", func(t *testing.T) {
		// Create request with headers that should be modified
		req, err := http.NewRequest("GET", "http://localhost:8082/headers", nil)
		require.NoError(t, err)

		// Add headers that should be removed by middleware
		req.Header.Set("X-Forwarded-For", "192.168.1.1")
		req.Header.Set("X-Real-IP", "10.0.0.1")
		req.Header.Set("User-Agent", "test-agent")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err, "Failed to make request")
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()

		// Check response headers
		assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

		// Read and parse response body
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Failed to read response body")

		var headerResponse HeaderResponse
		err = json.Unmarshal(body, &headerResponse)
		require.NoError(t, err, "Failed to unmarshal response")

		// Check that request headers were properly modified
		headerMap := make(map[string][]string)
		for _, h := range headerResponse.RequestHeaders {
			headerMap[h.Name] = h.Values
		}

		// Headers that should be removed
		assert.NotContains(t, headerMap, "X-Forwarded-For", "X-Forwarded-For should be removed")
		assert.NotContains(t, headerMap, "X-Real-IP", "X-Real-IP should be removed")

		// Headers that should be added/set by middleware
		assert.Contains(t, headerMap, "X-Request-Source")
		assert.Equal(t, []string{"go-supervisor-example"}, headerMap["X-Request-Source"])
		assert.Contains(t, headerMap, "X-Internal-Request")
		assert.Equal(t, []string{"true"}, headerMap["X-Internal-Request"])
		assert.Contains(t, headerMap, "X-Processing-Time")

		// User-Agent should still be present (not removed in main middleware)
		assert.Contains(t, headerMap, "User-Agent")

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("TestSpecialRoute", func(t *testing.T) {
		// Create request with User-Agent that should be removed by special route middleware
		req, err := http.NewRequest("GET", "http://localhost:8082/special", nil)
		require.NoError(t, err)
		req.Header.Set("User-Agent", "test-agent")

		client := &http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err, "Failed to make request")
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()

		// Check special route response headers
		assert.Equal(t, "special", resp.Header.Get("X-Route-Type"))
		assert.Equal(t, "route-specific-value", resp.Header.Get("X-Different-Header"))

		// Read and parse response body
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "Failed to read response body")

		var headerResponse HeaderResponse
		err = json.Unmarshal(body, &headerResponse)
		require.NoError(t, err, "Failed to unmarshal response")

		// Check that User-Agent was removed by special route middleware
		headerMap := make(map[string][]string)
		for _, h := range headerResponse.RequestHeaders {
			headerMap[h.Name] = h.Values
		}

		assert.NotContains(
			t,
			headerMap,
			"User-Agent",
			"User-Agent should be removed by special route",
		)
		assert.Contains(t, headerMap, "X-Route-Specific")
		assert.Equal(t, []string{"different-route"}, headerMap["X-Route-Specific"])

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	// Stop the supervisor
	sv.Shutdown()

	// Wait for server to stop or timeout
	select {
	case err := <-errCh:
		// This is expected when shutdown is called
		if err != nil {
			t.Logf("Server stopped with: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Server did not stop within timeout")
	}
}

// TestBuildRoutes tests route building in isolation
func TestBuildRoutes(t *testing.T) {
	t.Parallel()

	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})

	routes, err := buildRoutes(logHandler)
	require.NoError(t, err, "buildRoutes should not return an error")
	require.Len(t, routes, 3, "Should have exactly 3 routes")

	// Check route paths since route names are not publicly accessible
	routePaths := make([]string, len(routes))
	for i, route := range routes {
		routePaths[i] = route.Path
	}

	assert.Contains(t, routePaths, "/")
	assert.Contains(t, routePaths, "/headers")
	assert.Contains(t, routePaths, "/special")
}
