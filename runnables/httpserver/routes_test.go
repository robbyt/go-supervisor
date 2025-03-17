package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRoute(t *testing.T) {
	t.Parallel()

	testHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	tests := []struct {
		name        string
		routeName   string
		path        string
		handler     http.HandlerFunc
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid Route",
			routeName:   "test-route",
			path:        "/test",
			handler:     testHandler,
			expectError: false,
		},
		{
			name:        "Empty Name",
			routeName:   "",
			path:        "/test",
			handler:     testHandler,
			expectError: true,
			errorMsg:    "name cannot be empty",
		},
		{
			name:        "Empty Path",
			routeName:   "test-route",
			path:        "",
			handler:     testHandler,
			expectError: true,
			errorMsg:    "path cannot be empty",
		},
		{
			name:        "Nil Handler",
			routeName:   "test-route",
			path:        "/test",
			handler:     nil,
			expectError: true,
			errorMsg:    "handler cannot be nil",
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			route, err := NewRoute(tt.routeName, tt.path, tt.handler)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, route)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, route)
				assert.Equal(t, tt.routeName, route.name)
				assert.Equal(t, tt.path, route.Path)
				assert.NotNil(t, route.Handler)
			}
		})
	}
}

func TestNewWildcardRoute(t *testing.T) {
	t.Parallel()

	responseText := "wildcard handler called"
	testHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(responseText))
		require.NoError(t, err)
	}

	tests := []struct {
		name        string
		prefix      string
		handler     http.HandlerFunc
		testPath    string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Valid Wildcard Route with Slash",
			prefix:      "/api/",
			handler:     testHandler,
			testPath:    "/api/users/123",
			expectError: false,
		},
		{
			name:        "Valid Wildcard Route without Slash",
			prefix:      "/api",
			handler:     testHandler,
			testPath:    "/api/users/123",
			expectError: false,
		},
		{
			name:        "Valid Wildcard Route without Leading Slash",
			prefix:      "api",
			handler:     testHandler,
			testPath:    "/api/users/123",
			expectError: false,
		},
		{
			name:        "Empty Prefix",
			prefix:      "",
			handler:     testHandler,
			expectError: true,
			errorMsg:    "prefix cannot be empty",
		},
		{
			name:        "Nil Handler",
			prefix:      "/api/",
			handler:     nil,
			expectError: true,
			errorMsg:    "handler cannot be nil",
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			route, err := NewWildcardRoute(tt.prefix, tt.handler)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, route)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, route)
				assert.Contains(t, route.name, "wildcard:")
				assert.NotNil(t, route.Handler)

				// Test the handler with a sample request
				req := httptest.NewRequest("GET", tt.testPath, nil)
				w := httptest.NewRecorder()

				route.Handler(w, req)
				resp := w.Result()
				defer resp.Body.Close()

				// The wildcard handler should respond successfully
				assert.Equal(t, http.StatusOK, resp.StatusCode)
			}
		})
	}

	// Test specific behavior of the StripPrefix functionality
	t.Run("StripPrefix functionality", func(t *testing.T) {
		var actualPath string
		pathCaptureHandler := func(w http.ResponseWriter, r *http.Request) {
			actualPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
		}

		route, err := NewWildcardRoute("/api/", pathCaptureHandler)
		require.NoError(t, err)

		// Test with a request to a subpath
		req := httptest.NewRequest("GET", "/api/users/123", nil)
		w := httptest.NewRecorder()
		route.Handler(w, req)

		// The handler should have received the stripped path
		assert.Equal(t, "users/123", actualPath)
	})

	// Test 404 behavior for non-matching paths
	t.Run("Returns 404 for non-matching paths", func(t *testing.T) {
		handlerCalled := false
		handler := func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
		}

		route, err := NewWildcardRoute("/api/", handler)
		require.NoError(t, err)

		// Test with a request to a different path
		req := httptest.NewRequest("GET", "/different/path", nil)
		w := httptest.NewRecorder()
		route.Handler(w, req)

		// The handler should not be called, and a 404 should be returned
		assert.False(t, handlerCalled)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
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
			name: "Same routes",
			route1: Route{
				name:    "v1",
				Path:    "/test",
				Handler: sameHandler,
			},
			route2: Route{
				name:    "v1",
				Path:    "/test",
				Handler: sameHandler,
			},
			expected: true,
		},
		{
			name: "Different paths",
			route1: Route{
				name:    "v1",
				Path:    "/test1",
				Handler: sameHandler,
			},
			route2: Route{
				name:    "v1",
				Path:    "/test2",
				Handler: sameHandler,
			},
			expected: false,
		},
		{
			name: "Different names",
			route1: Route{
				name:    "v1",
				Path:    "/test",
				Handler: sameHandler,
			},
			route2: Route{
				name:    "v2",
				Path:    "/test",
				Handler: sameHandler,
			},
			expected: false,
		},
		{
			name: "Different handlers",
			route1: Route{
				name:    "v1",
				Path:    "/test",
				Handler: func(w http.ResponseWriter, r *http.Request) {},
			},
			route2: Route{
				name:    "v1",
				Path:    "/test",
				Handler: func(w http.ResponseWriter, r *http.Request) {},
			},
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
				{
					name:    "v1",
					Path:    "/test",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			expected: "Routes<Name: v1, Path: /test>",
		},
		{
			name: "MultipleRoutes",
			routes: Routes{
				{
					name:    "v1",
					Path:    "/test1",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
				{
					name:    "v2",
					Path:    "/test2",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
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
				{
					name:    "v1",
					Path:    "/test",
					Handler: sameHandler,
				},
			},
			routes2: Routes{
				{
					name:    "v1",
					Path:    "/test",
					Handler: sameHandler,
				},
			},
			expected: true,
		},
		{
			name: "DifferentLengths",
			routes1: Routes{
				{
					name:    "v1",
					Path:    "/test1",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
				{
					name:    "v2",
					Path:    "/test2",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			routes2: Routes{
				{
					name:    "v1",
					Path:    "/test1",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			expected: false,
		},
		{
			name: "DifferentPaths",
			routes1: Routes{
				{
					name:    "v1",
					Path:    "/test1",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			routes2: Routes{
				{
					name:    "v1",
					Path:    "/test2",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			expected: false,
		},
		{
			name: "DifferentHandlers",
			routes1: Routes{
				{
					name:    "v1",
					Path:    "/test",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			routes2: Routes{
				{
					name: "v1",
					Path: "/test",
					Handler: func(w http.ResponseWriter, r *http.Request) {
						_, err := w.Write([]byte("different"))
						require.NoError(t, err)
					},
				},
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
				{
					name:    "v1",
					Path:    "/test",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			expected: false,
		},
		{
			name: "Same Routes in Different Order",
			routes1: Routes{
				{
					name:    "v1",
					Path:    "/test1",
					Handler: sameHandler,
				},
				{
					name:    "v2",
					Path:    "/test2",
					Handler: sameHandler,
				},
			},
			routes2: Routes{
				{
					name:    "v2",
					Path:    "/test2",
					Handler: sameHandler,
				},
				{
					name:    "v1",
					Path:    "/test1",
					Handler: sameHandler,
				},
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

func TestApplyMiddlewares(t *testing.T) {
	// Create a test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("test response"))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
			return
		}
	})

	// Create middleware that adds headers
	middleware1 := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Middleware-1", "applied")
			next(w, r)
		}
	}

	middleware2 := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Middleware-2", "applied")
			next(w, r)
		}
	}

	// Apply middlewares
	wrappedHandler := applyMiddlewares(testHandler, middleware1, middleware2)

	// Create a test request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	// Call the handler
	wrappedHandler(w, req)

	// Check the response
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "applied", resp.Header.Get("X-Middleware-1"))
	assert.Equal(t, "applied", resp.Header.Get("X-Middleware-2"))
}
