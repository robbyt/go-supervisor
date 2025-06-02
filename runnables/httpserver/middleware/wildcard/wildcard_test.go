package wildcard

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

// createPathEchoHandler returns a handler that echoes back the request path
func createPathEchoHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		n, err := w.Write([]byte(r.URL.Path))
		assert.NoError(t, err)
		assert.Equal(t, len(r.URL.Path), n)
	}
}

// executeHandlerWithWildcard runs the provided handler with the wildcard middleware
func executeHandlerWithWildcard(
	t *testing.T,
	handler http.HandlerFunc,
	prefix string,
	rec *httptest.ResponseRecorder,
	req *http.Request,
) {
	t.Helper()
	// Create route with wildcard middleware and the handler
	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test", handler, New(prefix))
	assert.NoError(t, err)
	route.ServeHTTP(rec, req)
}

func TestNew(t *testing.T) {
	middleware := New("/api/")
	assert.NotNil(t, middleware)
}

func TestWildcard_MatchingPrefix(t *testing.T) {
	t.Parallel()
	handler := createPathEchoHandler(t)
	rec, req := setupRequest(t, "GET", "/api/users/123")

	executeHandlerWithWildcard(t, handler, "/api/", rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "users/123", rec.Body.String())
}

func TestWildcard_ExactPrefixMatch(t *testing.T) {
	t.Parallel()
	handler := createPathEchoHandler(t)
	rec, req := setupRequest(t, "GET", "/api/")

	executeHandlerWithWildcard(t, handler, "/api/", rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", rec.Body.String())
}

func TestWildcard_NonMatchingPrefix(t *testing.T) {
	t.Parallel()
	handler := createPathEchoHandler(t)
	rec, req := setupRequest(t, "GET", "/other/users")

	executeHandlerWithWildcard(t, handler, "/api/", rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "404 page not found")
}

func TestWildcard_PartialPrefixMatch(t *testing.T) {
	t.Parallel()
	handler := createPathEchoHandler(t)
	rec, req := setupRequest(t, "GET", "/ap")

	executeHandlerWithWildcard(t, handler, "/api/", rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "404 page not found")
}

func TestWildcard_EmptyPath(t *testing.T) {
	t.Parallel()
	handler := createPathEchoHandler(t)

	// Create request with minimal valid URL but empty path
	req := httptest.NewRequest("GET", "http://localhost", nil)
	req.URL.Path = ""
	rec := httptest.NewRecorder()

	executeHandlerWithWildcard(t, handler, "/api/", rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWildcard_RootPath(t *testing.T) {
	t.Parallel()
	handler := createPathEchoHandler(t)
	rec, req := setupRequest(t, "GET", "/")

	executeHandlerWithWildcard(t, handler, "/", rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "", rec.Body.String())
}

func TestWildcard_PrefixWithoutTrailingSlash(t *testing.T) {
	t.Parallel()
	handler := createPathEchoHandler(t)
	rec, req := setupRequest(t, "GET", "/api/users")

	executeHandlerWithWildcard(t, handler, "/api", rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "users", rec.Body.String())
}

func TestWildcard_RequestProcessorAbort(t *testing.T) {
	t.Parallel()
	var nextCalled bool

	// Create a handler that should not be called when path doesn't match
	handler := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}

	// Test with non-matching path to verify abort behavior
	rec, req := setupRequest(t, "GET", "/different/path")

	// Use the helper function which creates a complete route
	executeHandlerWithWildcard(t, handler, "/api/", rec, req)

	// Verify that the handler was not called (middleware aborted)
	assert.False(t, nextCalled)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestWildcard_QueryParameters(t *testing.T) {
	t.Parallel()
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(r.URL.Path + "?" + r.URL.RawQuery))
		assert.NoError(t, err)
	}

	rec, req := setupRequest(t, "GET", "/api/users?id=123&name=test")

	executeHandlerWithWildcard(t, handler, "/api/", rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "users?id=123&name=test", rec.Body.String())
}

func TestWildcard_DifferentMethods(t *testing.T) {
	t.Parallel()
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(r.Method))
		assert.NoError(t, err)
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			rec, req := setupRequest(t, method, "/api/test")
			executeHandlerWithWildcard(t, handler, "/api/", rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, method, rec.Body.String())
		})
	}
}

func TestWildcard_NestedPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		prefix   string
		path     string
		expected string
	}{
		{
			name:     "single level",
			prefix:   "/api/",
			path:     "/api/users",
			expected: "users",
		},
		{
			name:     "multi level",
			prefix:   "/api/v1/",
			path:     "/api/v1/users/123/posts",
			expected: "users/123/posts",
		},
		{
			name:     "deep nesting",
			prefix:   "/app/api/v2/",
			path:     "/app/api/v2/admin/users/123/settings/profile",
			expected: "admin/users/123/settings/profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createPathEchoHandler(t)
			rec, req := setupRequest(t, "GET", tt.path)

			executeHandlerWithWildcard(t, handler, tt.prefix, rec, req)

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.expected, rec.Body.String())
		})
	}
}

func TestWildcard_SpecialCharacters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		prefix   string
		path     string
		expected string
		status   int
	}{
		{
			name:     "url encoded",
			prefix:   "/api/",
			path:     "/api/users%20with%20spaces",
			expected: "users with spaces",
			status:   http.StatusOK,
		},
		{
			name:     "unicode characters",
			prefix:   "/api/",
			path:     "/api/用户/测试",
			expected: "用户/测试",
			status:   http.StatusOK,
		},
		{
			name:     "special symbols",
			prefix:   "/api/",
			path:     "/api/test-path_with.symbols",
			expected: "test-path_with.symbols",
			status:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createPathEchoHandler(t)
			rec, req := setupRequest(t, "GET", tt.path)

			executeHandlerWithWildcard(t, handler, tt.prefix, rec, req)

			assert.Equal(t, tt.status, rec.Code)
			if tt.status == http.StatusOK {
				assert.Equal(t, tt.expected, rec.Body.String())
			}
		})
	}
}

func TestWildcard_PrefixNormalization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		prefix   string
		path     string
		expected string
		status   int
	}{
		{
			name:     "empty prefix defaults to root",
			prefix:   "",
			path:     "/users",
			expected: "users",
			status:   http.StatusOK,
		},
		{
			name:     "prefix without leading slash",
			prefix:   "api",
			path:     "/api/users",
			expected: "users",
			status:   http.StatusOK,
		},
		{
			name:     "prefix without trailing slash",
			prefix:   "/api",
			path:     "/api/users",
			expected: "users",
			status:   http.StatusOK,
		},
		{
			name:     "prefix without leading or trailing slash",
			prefix:   "api",
			path:     "/api/users",
			expected: "users",
			status:   http.StatusOK,
		},
		{
			name:     "root prefix stays as root",
			prefix:   "/",
			path:     "/users",
			expected: "users",
			status:   http.StatusOK,
		},
		{
			name:     "malformed prefix gets normalized",
			prefix:   "api/v1",
			path:     "/api/v1/users",
			expected: "users",
			status:   http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createPathEchoHandler(t)
			rec, req := setupRequest(t, "GET", tt.path)

			executeHandlerWithWildcard(t, handler, tt.prefix, rec, req)

			assert.Equal(t, tt.status, rec.Code)
			if tt.status == http.StatusOK {
				assert.Equal(t, tt.expected, rec.Body.String())
			}
		})
	}
}

func TestWildcard_EdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		prefix   string
		path     string
		expected string
		status   int
	}{
		{
			name:     "double slash in prefix",
			prefix:   "//api//",
			path:     "//api//users",
			expected: "users",
			status:   http.StatusOK,
		},
		{
			name:     "empty path with root prefix",
			prefix:   "/",
			path:     "/",
			expected: "",
			status:   http.StatusOK,
		},
		{
			name:     "exact match with normalized prefix",
			prefix:   "api",
			path:     "/api/",
			expected: "",
			status:   http.StatusOK,
		},
		{
			name:     "path shorter than normalized prefix",
			prefix:   "api/v1",
			path:     "/api",
			expected: "",
			status:   http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := createPathEchoHandler(t)
			rec, req := setupRequest(t, "GET", tt.path)

			executeHandlerWithWildcard(t, handler, tt.prefix, rec, req)

			assert.Equal(t, tt.status, rec.Code)
			if tt.status == http.StatusOK {
				assert.Equal(t, tt.expected, rec.Body.String())
			}
		})
	}
}
