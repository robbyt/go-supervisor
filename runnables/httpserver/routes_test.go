package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testRoute creates a Route for testing purposes
func testRoute(t *testing.T, name, path string, handler http.HandlerFunc) Route {
	t.Helper()
	route, err := NewRouteFromHandlerFunc(name, path, handler)
	require.NoError(t, err)
	return *route
}

func TestNewRoute_Internal(t *testing.T) {
	t.Parallel()

	testHandler := func(rp *RequestProcessor) {
		rp.Writer().WriteHeader(http.StatusOK)
	}

	tests := []struct {
		name        string
		routeName   string
		path        string
		handlers    []HandlerFunc
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid Route with Single Handler",
			routeName:   "test-route",
			path:        "/test",
			handlers:    []HandlerFunc{testHandler},
			expectError: false,
		},
		{
			name:        "Valid Route with Multiple Handlers",
			routeName:   "test-route",
			path:        "/test",
			handlers:    []HandlerFunc{testHandler, testHandler},
			expectError: false,
		},
		{
			name:        "Empty Name",
			routeName:   "",
			path:        "/test",
			handlers:    []HandlerFunc{testHandler},
			expectError: true,
			errorMsg:    "name cannot be empty",
		},
		{
			name:        "Empty Path",
			routeName:   "test-route",
			path:        "",
			handlers:    []HandlerFunc{testHandler},
			expectError: true,
			errorMsg:    "path cannot be empty",
		},
		{
			name:        "No Handlers",
			routeName:   "test-route",
			path:        "/test",
			handlers:    []HandlerFunc{},
			expectError: true,
			errorMsg:    "at least one handler required",
		},
		{
			name:        "Nil Handlers Slice",
			routeName:   "test-route",
			path:        "/test",
			handlers:    nil,
			expectError: true,
			errorMsg:    "at least one handler required",
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			route, err := newRoute(tt.routeName, tt.path, tt.handlers...)

			if tt.expectError {
				assert.Error(t, err, "should return error for invalid input")
				assert.Nil(t, route, "should return nil route on error")
				assert.Contains(
					t,
					err.Error(),
					tt.errorMsg,
					"error message should contain expected text",
				)
			} else {
				assert.NoError(t, err, "should not return error for valid input")
				assert.NotNil(t, route, "should return non-nil route")
				assert.Equal(t, tt.path, route.Path, "route path should match input")
				assert.Equal(t, len(tt.handlers), len(route.Handlers), "handler count should match")
			}
		})
	}
}

func TestRoute_Equal(t *testing.T) {
	t.Parallel()

	sameHandler := func(w http.ResponseWriter, r *http.Request) {}

	tests := []struct {
		name     string
		route1   Route
		route2   Route
		expected bool
	}{
		{
			name:     "Same routes",
			route1:   testRoute(t, "v1", "/test", sameHandler),
			route2:   testRoute(t, "v1", "/test", sameHandler),
			expected: true,
		},
		{
			name:     "Different paths",
			route1:   testRoute(t, "v1", "/test1", sameHandler),
			route2:   testRoute(t, "v1", "/test2", sameHandler),
			expected: false,
		},
		{
			name:     "Different names",
			route1:   testRoute(t, "v1", "/test", sameHandler),
			route2:   testRoute(t, "v2", "/test", sameHandler),
			expected: false,
		},
		{
			name:     "Different handlers",
			route1:   testRoute(t, "v1", "/test", func(w http.ResponseWriter, r *http.Request) {}),
			route2:   testRoute(t, "v1", "/test", func(w http.ResponseWriter, r *http.Request) {}),
			expected: true, // Route equality no longer compares handler functions
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.route1.Equal(tt.route2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRoutes_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		routes   Routes
		expected string
	}{
		{
			name:     "EmptyRoutes",
			routes:   Routes{},
			expected: "Routes<>",
		},
		{
			name: "SingleRoute",
			routes: Routes{
				testRoute(t, "v1", "/test", func(w http.ResponseWriter, r *http.Request) {}),
			},
			expected: "Routes<Name: v1, Path: /test>",
		},
		{
			name: "MultipleRoutes",
			routes: Routes{
				testRoute(t, "v1", "/test1", func(w http.ResponseWriter, r *http.Request) {}),
				testRoute(t, "v2", "/test2", func(w http.ResponseWriter, r *http.Request) {}),
			},
			expected: "Routes<Name: v1, Path: /test1, Name: v2, Path: /test2>",
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.routes.String())
		})
	}
}

func TestRoutes_Equal(t *testing.T) {
	t.Parallel()

	// the .Equal check looks at the memory space, to see if the handler is the SAME object in memory
	sameHandler := func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("same"))
		require.NoError(t, err)
	}

	tests := []struct {
		name     string
		routes1  Routes
		routes2  Routes
		expected bool
	}{
		{
			name:     "BothEmpty",
			routes1:  Routes{},
			routes2:  Routes{},
			expected: true,
		},
		{
			name: "SameRoutes",
			routes1: Routes{
				testRoute(t, "v1", "/test", sameHandler),
			},
			routes2: Routes{
				testRoute(t, "v1", "/test", sameHandler),
			},
			expected: true,
		},
		{
			name: "DifferentLengths",
			routes1: Routes{
				testRoute(t, "v1", "/test1", func(w http.ResponseWriter, r *http.Request) {}),
				testRoute(t, "v2", "/test2", func(w http.ResponseWriter, r *http.Request) {}),
			},
			routes2: Routes{
				testRoute(t, "v1", "/test1", func(w http.ResponseWriter, r *http.Request) {}),
			},
			expected: false,
		},
		{
			name: "DifferentPaths",
			routes1: Routes{
				testRoute(t, "v1", "/test1", func(w http.ResponseWriter, r *http.Request) {}),
			},
			routes2: Routes{
				testRoute(t, "v1", "/test2", func(w http.ResponseWriter, r *http.Request) {}),
			},
			expected: false,
		},
		{
			name: "DifferentHandlers",
			routes1: Routes{
				testRoute(t, "v1", "/test", func(w http.ResponseWriter, r *http.Request) {}),
			},
			routes2: Routes{
				testRoute(t, "v1", "/test", func(w http.ResponseWriter, r *http.Request) {
					_, err := w.Write([]byte("different"))
					require.NoError(t, err)
				}),
			},
			expected: true,
		},
		{
			name:     "NilRoutes",
			routes1:  nil,
			routes2:  nil,
			expected: true,
		},
		{
			name:    "OneNilRoute",
			routes1: nil,
			routes2: Routes{
				testRoute(t, "v1", "/test", func(w http.ResponseWriter, r *http.Request) {}),
			},
			expected: false,
		},
		{
			name: "Same Routes in Different Order",
			routes1: Routes{
				testRoute(t, "v1", "/test1", sameHandler),
				testRoute(t, "v2", "/test2", sameHandler),
			},
			routes2: Routes{
				testRoute(t, "v2", "/test2", sameHandler),
				testRoute(t, "v1", "/test1", sameHandler),
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.routes1.Equal(tt.routes2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNewRouteFromHandlerFuncWithMiddleware(t *testing.T) {
	t.Parallel()

	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("test response"))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
			return
		}
	})

	// Create middleware that adds headers - using HandlerFunc type
	middleware1 := func(rp *RequestProcessor) {
		rp.Writer().Header().Set("X-Middleware-1", "applied")
		rp.Next()
	}

	middleware2 := func(rp *RequestProcessor) {
		rp.Writer().Header().Set("X-Middleware-2", "applied")
		rp.Next()
	}

	tests := []struct {
		name        string
		routeName   string
		path        string
		handler     http.HandlerFunc
		middlewares []HandlerFunc
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid Route with Middlewares",
			routeName:   "test-route",
			path:        "/test",
			handler:     testHandler,
			middlewares: []HandlerFunc{middleware1, middleware2},
			expectError: false,
		},
		{
			name:        "Valid Route with No Middlewares",
			routeName:   "test-route",
			path:        "/test",
			handler:     testHandler,
			middlewares: []HandlerFunc{},
			expectError: false,
		},
		{
			name:        "Empty Name",
			routeName:   "",
			path:        "/test",
			handler:     testHandler,
			middlewares: []HandlerFunc{middleware1},
			expectError: true,
			errorMsg:    "name cannot be empty",
		},
		{
			name:        "Empty Path",
			routeName:   "test-route",
			path:        "",
			handler:     testHandler,
			middlewares: []HandlerFunc{middleware1},
			expectError: true,
			errorMsg:    "path cannot be empty",
		},
		{
			name:        "Nil Handler",
			routeName:   "test-route",
			path:        "/test",
			handler:     nil,
			middlewares: []HandlerFunc{middleware1},
			expectError: true,
			errorMsg:    "handler cannot be nil",
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			route, err := NewRouteFromHandlerFunc(
				tt.routeName,
				tt.path,
				tt.handler,
				tt.middlewares...)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, route)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, route)
				assert.Equal(t, tt.path, route.Path)
				assert.NotEmpty(t, route.Handlers)

				// Test the route using ServeHTTP method
				req := httptest.NewRequest("GET", tt.path, nil)
				w := httptest.NewRecorder()
				route.ServeHTTP(w, req)
				resp := w.Result()
				defer func() { assert.NoError(t, resp.Body.Close()) }()

				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Verify middlewares were applied in the correct order
				if len(tt.middlewares) > 0 {
					assert.Equal(t, "applied", resp.Header.Get("X-Middleware-1"))
				}
				if len(tt.middlewares) > 1 {
					assert.Equal(t, "applied", resp.Header.Get("X-Middleware-2"))
				}
			}
		})
	}

	// Test middleware execution order
	t.Run("Middleware Execution Order", func(t *testing.T) {
		t.Parallel()

		var executionOrder []string

		testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			executionOrder = append(executionOrder, "handler")
			w.WriteHeader(http.StatusOK)
		})

		middlewareA := func(rp *RequestProcessor) {
			executionOrder = append(executionOrder, "middlewareA-before")
			rp.Next()
			executionOrder = append(executionOrder, "middlewareA-after")
		}

		middlewareB := func(rp *RequestProcessor) {
			executionOrder = append(executionOrder, "middlewareB-before")
			rp.Next()
			executionOrder = append(executionOrder, "middlewareB-after")
		}

		route, err := NewRouteFromHandlerFunc(
			"order-test",
			"/order-test",
			testHandler,
			middlewareA,
			middlewareB,
		)
		require.NoError(t, err)

		req := httptest.NewRequest("GET", "/order-test", nil)
		w := httptest.NewRecorder()
		route.ServeHTTP(w, req)

		// The expected order is: middlewareA before, middlewareB before, handler, middlewareB after, middlewareA after
		expectedOrder := []string{
			"middlewareA-before",
			"middlewareB-before",
			"handler",
			"middlewareB-after",
			"middlewareA-after",
		}
		assert.Equal(t, expectedOrder, executionOrder)
	})
}
