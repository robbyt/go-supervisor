package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildRoutes(t *testing.T) {
	t.Parallel()

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError, // Reduce noise in tests
	})

	routes, err := buildRoutes(handler)
	require.NoError(t, err, "building routes should not fail")
	require.Len(t, routes, 5, "should create 5 routes")

	// Verify route names and paths
	expectedRoutes := map[string]string{
		"index":      "/",
		"json-data":  "/api/data",
		"html-demo":  "/html",
		"error-demo": "/error",
		"panic-demo": "/panic",
	}

	routeMap := make(map[string]string)
	for _, route := range routes {
		routeMap[route.Path] = route.Path
	}

	for name, path := range expectedRoutes {
		assert.Contains(
			t,
			routeMap,
			path,
			fmt.Sprintf("route %s should exist at path %s", name, path),
		)
	}
}

func TestRouteResponses(t *testing.T) {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	})

	routes, err := buildRoutes(handler)
	require.NoError(t, err, "building routes should not fail")

	// Create a test server
	mux := http.NewServeMux()
	for _, route := range routes {
		mux.HandleFunc(route.Path, route.ServeHTTP)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		checkJSON      bool
		checkContent   string
	}{
		{
			name:           "index route converts text to JSON",
			path:           "/",
			expectedStatus: http.StatusOK,
			checkJSON:      true,
			checkContent:   "Welcome to the JSON API example server!",
		},
		{
			name:           "API route preserves JSON",
			path:           "/api/data",
			expectedStatus: http.StatusOK,
			checkJSON:      true,
			checkContent:   "This is already JSON",
		},
		{
			name:           "HTML route converts to JSON",
			path:           "/html",
			expectedStatus: http.StatusOK,
			checkJSON:      true,
			checkContent:   "Hello from HTML", // Just check for part of the content, JSON escapes HTML
		},
		{
			name:           "error route returns JSON error",
			path:           "/error",
			expectedStatus: http.StatusNotFound,
			checkJSON:      true,
			checkContent:   "The requested resource was not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + tt.path)
			require.NoError(t, err, "HTTP request should not fail")
			defer func() {
				assert.NoError(t, resp.Body.Close())
			}()

			assert.Equal(t, tt.expectedStatus, resp.StatusCode, "status code should match")
			assert.Equal(
				t,
				"application/json",
				resp.Header.Get("Content-Type"),
				"content type should be JSON",
			)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err, "reading response body should not fail")

			if tt.checkJSON {
				var jsonData any
				err := json.Unmarshal(body, &jsonData)
				assert.NoError(t, err, "response should be valid JSON")
			}

			if tt.checkContent != "" {
				assert.Contains(
					t,
					string(body),
					tt.checkContent,
					"response should contain expected content",
				)
			}

			// Check security headers
			assert.Equal(
				t,
				"nosniff",
				resp.Header.Get("X-Content-Type-Options"),
				"security headers should be set",
			)
			assert.Equal(
				t,
				"DENY",
				resp.Header.Get("X-Frame-Options"),
				"frame options should be set",
			)

			// Check CORS headers for API endpoints
			if strings.HasPrefix(tt.path, "/api/") {
				assert.Equal(
					t,
					"*",
					resp.Header.Get("Access-Control-Allow-Origin"),
					"CORS headers should be set for API endpoints",
				)
			}
		})
	}
}

func TestJSONEnforcerIntegration(t *testing.T) {
	t.Parallel()

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	})

	routes, err := buildRoutes(handler)
	require.NoError(t, err, "building routes should not fail")

	// Create a test server
	mux := http.NewServeMux()
	for _, route := range routes {
		mux.HandleFunc(route.Path, route.ServeHTTP)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("text response is wrapped in JSON", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err, "HTTP request should not fail")
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "reading response body should not fail")

		var jsonResp map[string]any
		err = json.Unmarshal(body, &jsonResp)
		require.NoError(t, err, "response should be valid JSON")

		response, ok := jsonResp["response"].(string)
		require.True(t, ok, "response field should be a string")
		assert.Contains(
			t,
			response,
			"Welcome to the JSON API example server!",
			"original text should be in response field",
		)
	})

	t.Run("JSON response is preserved", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/api/data")
		require.NoError(t, err, "HTTP request should not fail")
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "reading response body should not fail")

		var jsonResp map[string]any
		err = json.Unmarshal(body, &jsonResp)
		require.NoError(t, err, "response should be valid JSON")

		// Should have direct fields, not wrapped in "response"
		message, ok := jsonResp["message"].(string)
		require.True(t, ok, "message field should exist and be a string")
		assert.Contains(
			t,
			message,
			"This is already JSON",
			"original JSON structure should be preserved",
		)

		status, ok := jsonResp["status"].(string)
		require.True(t, ok, "status field should exist and be a string")
		assert.Equal(t, "success", status, "status should be preserved")
	})

	t.Run("HTML response is wrapped in JSON", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/html")
		require.NoError(t, err, "HTTP request should not fail")
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "reading response body should not fail")

		var jsonResp map[string]any
		err = json.Unmarshal(body, &jsonResp)
		require.NoError(t, err, "response should be valid JSON")

		response, ok := jsonResp["response"].(string)
		require.True(t, ok, "response field should be a string")
		assert.Contains(
			t,
			response,
			"Hello from HTML",
			"HTML should be preserved in response field",
		)
	})

	t.Run("panic route triggers recovery middleware", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/panic")
		require.NoError(t, err, "HTTP request should not fail")
		defer func() {
			assert.NoError(t, resp.Body.Close())
		}()

		// Panic recovery returns 500 with text/plain
		assert.Equal(
			t,
			http.StatusInternalServerError,
			resp.StatusCode,
			"should return 500 for panic",
		)
		assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"),
			"panic recovery returns text/plain, not JSON")

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err, "reading response body should not fail")
		assert.Equal(t, "Internal Server Error\n", string(body),
			"panic recovery returns standard error message")

		// Security headers should still be set (from middleware before panic)
		assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	})
}

func TestRunServer(t *testing.T) {
	t.Parallel()

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	})

	routes, err := buildRoutes(handler)
	require.NoError(t, err, "building routes should not fail")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	httpServer, err := createHTTPServer(routes, handler)
	require.NoError(t, err, "creating HTTP server should not fail")

	sv, err := createSupervisor(ctx, handler, httpServer)
	require.NoError(t, err, "creating supervisor should not fail")
	require.NotNil(t, sv, "supervisor should not be nil")

	// Test that the supervisor can be started and stopped
	// We'll start it in a goroutine and then stop it
	done := make(chan error, 1)
	go func() {
		done <- sv.Run()
	}()

	// Give the server a moment to start
	time.Sleep(200 * time.Millisecond)

	// Cancel the context to stop the server
	cancel()

	// Wait for the server to stop
	select {
	case err := <-done:
		// The supervisor should stop cleanly when context is cancelled
		assert.NoError(t, err, "supervisor should stop cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop within timeout")
	}
}

func TestHeadersMiddlewareIntegration(t *testing.T) {
	t.Parallel()

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	})

	routes, err := buildRoutes(handler)
	require.NoError(t, err, "building routes should not fail")

	// Create a test server
	mux := http.NewServeMux()
	for _, route := range routes {
		mux.HandleFunc(route.Path, route.ServeHTTP)
	}
	server := httptest.NewServer(mux)
	defer server.Close()

	t.Run("all responses have JSON headers", func(t *testing.T) {
		paths := []string{"/", "/api/data", "/html", "/error"}

		for _, path := range paths {
			resp, err := http.Get(server.URL + path)
			require.NoError(t, err, fmt.Sprintf("HTTP request to %s should not fail", path))
			assert.NoError(t, resp.Body.Close())

			assert.Equal(t, "application/json", resp.Header.Get("Content-Type"),
				fmt.Sprintf("path %s should have JSON content type", path))
			assert.Equal(t, "no-cache", resp.Header.Get("Cache-Control"),
				fmt.Sprintf("path %s should have no-cache header", path))
		}
	})

	t.Run("API endpoints have CORS headers", func(t *testing.T) {
		apiPaths := []string{"/api/data"}

		for _, path := range apiPaths {
			resp, err := http.Get(server.URL + path)
			require.NoError(t, err, fmt.Sprintf("HTTP request to %s should not fail", path))
			assert.NoError(t, resp.Body.Close())

			assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"),
				fmt.Sprintf("API path %s should have CORS origin header", path))
			assert.Equal(
				t,
				"GET,POST,PUT,DELETE,OPTIONS",
				resp.Header.Get("Access-Control-Allow-Methods"),
				fmt.Sprintf("API path %s should have CORS methods header", path),
			)
		}
	})

	t.Run("all responses have security headers", func(t *testing.T) {
		paths := []string{"/", "/api/data", "/html", "/error", "/panic"}

		for _, path := range paths {
			resp, err := http.Get(server.URL + path)
			require.NoError(t, err, fmt.Sprintf("HTTP request to %s should not fail", path))
			assert.NoError(t, resp.Body.Close())

			assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"),
				fmt.Sprintf("path %s should have content type options header", path))
			assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"),
				fmt.Sprintf("path %s should have frame options header", path))
			assert.Equal(t, "1; mode=block", resp.Header.Get("X-XSS-Protection"),
				fmt.Sprintf("path %s should have XSS protection header", path))
		}
	})
}
