package headers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWithOperations(t *testing.T) {
	t.Parallel()

	t.Run("header removal", func(t *testing.T) {
		middleware := NewWithOperations(WithRemove("Server", "X-Powered-By"))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set some headers that should be removed
		rec.Header().Set("Server", "nginx/1.0")
		rec.Header().Set("X-Powered-By", "PHP/8.0")
		rec.Header().Set("Content-Type", "text/html")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Empty(t, rec.Header().Get("Server"), "Server header should be removed")
		assert.Empty(t, rec.Header().Get("X-Powered-By"), "X-Powered-By header should be removed")
		assert.Equal(t, "text/html", rec.Header().Get("Content-Type"), "Content-Type should remain")
	})

	t.Run("header addition vs setting", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSet(http.Header{"Set-Cookie": []string{"session=abc123"}}),
			WithAdd(http.Header{"Set-Cookie": []string{"theme=dark"}}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		cookies := rec.Header().Values("Set-Cookie")
		assert.Len(t, cookies, 2, "should have two Set-Cookie headers")
		assert.Contains(t, cookies, "session=abc123", "should contain session cookie")
		assert.Contains(t, cookies, "theme=dark", "should contain theme cookie")
	})

	t.Run("operation ordering: remove → set → add", func(t *testing.T) {
		middleware := NewWithOperations(
			WithRemove("X-Test"),
			WithSet(http.Header{"X-Test": []string{"set-value"}}),
			WithAdd(http.Header{"X-Test": []string{"add-value"}}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set header that should be removed first
		rec.Header().Set("X-Test", "original-value")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		values := rec.Header().Values("X-Test")
		assert.Len(t, values, 2, "should have two X-Test headers")
		assert.Contains(t, values, "set-value", "should contain set value")
		assert.Contains(t, values, "add-value", "should contain add value")
		assert.NotContains(t, values, "original-value", "should not contain removed value")
	})

	t.Run("multiple remove operations", func(t *testing.T) {
		middleware := NewWithOperations(
			WithRemove("Header1", "Header2"),
			WithRemove("Header3"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set headers that should be removed
		rec.Header().Set("Header1", "value1")
		rec.Header().Set("Header2", "value2")
		rec.Header().Set("Header3", "value3")
		rec.Header().Set("Header4", "value4")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Empty(t, rec.Header().Get("Header1"), "Header1 should be removed")
		assert.Empty(t, rec.Header().Get("Header2"), "Header2 should be removed")
		assert.Empty(t, rec.Header().Get("Header3"), "Header3 should be removed")
		assert.Equal(t, "value4", rec.Header().Get("Header4"), "Header4 should remain")
	})

	t.Run("multiple set operations", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSet(
				http.Header{"X-API": []string{"v1"}, "Content-Type": []string{"application/json"}},
			),
			WithSet(http.Header{"X-Version": []string{"1.0"}}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(t, "v1", rec.Header().Get("X-API"), "X-API should be set")
		assert.Equal(
			t,
			"application/json",
			rec.Header().Get("Content-Type"),
			"Content-Type should be set",
		)
		assert.Equal(t, "1.0", rec.Header().Get("X-Version"), "X-Version should be set")
	})

	t.Run("multiple add operations", func(t *testing.T) {
		middleware := NewWithOperations(
			WithAddHeader("X-Custom", "value1"),
			WithAddHeader("X-Custom", "value2"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		values := rec.Header().Values("X-Custom")
		assert.Len(t, values, 2, "should have two X-Custom headers")
		assert.Contains(t, values, "value1", "should contain value1")
		assert.Contains(t, values, "value2", "should contain value2")
	})

	t.Run("empty operations", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSet(http.Header{}),
			WithAdd(http.Header{}),
			WithRemove(),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Should not panic and should not affect existing headers
		assert.NotNil(t, rec.Header(), "headers should exist")
	})

	t.Run("remove non-existent headers", func(t *testing.T) {
		middleware := NewWithOperations(WithRemove("Non-Existent-Header"))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Should not panic
		assert.NotNil(t, rec.Header(), "headers should exist")
	})

	t.Run("complex scenario: security headers cleanup", func(t *testing.T) {
		middleware := NewWithOperations(
			WithRemove("Server", "X-Powered-By", "X-AspNet-Version"),
			WithSet(http.Header{
				"X-Content-Type-Options": []string{"nosniff"},
				"X-Frame-Options":        []string{"DENY"},
			}),
			WithAdd(http.Header{
				"Set-Cookie": []string{"secure=true; HttpOnly"},
			}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set some headers that should be removed
		rec.Header().Set("Server", "Apache/2.4")
		rec.Header().Set("X-Powered-By", "Express")
		rec.Header().Set("Set-Cookie", "session=abc123")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Verify removals
		assert.Empty(t, rec.Header().Get("Server"), "Server header should be removed")
		assert.Empty(t, rec.Header().Get("X-Powered-By"), "X-Powered-By header should be removed")

		// Verify sets
		assert.Equal(
			t,
			"nosniff",
			rec.Header().Get("X-Content-Type-Options"),
			"security header should be set",
		)
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"), "frame options should be set")

		// Verify adds
		cookies := rec.Header().Values("Set-Cookie")
		assert.Len(t, cookies, 2, "should have two Set-Cookie headers")
		assert.Contains(t, cookies, "session=abc123", "should contain original cookie")
		assert.Contains(t, cookies, "secure=true; HttpOnly", "should contain new secure cookie")
	})
}

func TestCORSWithFunctionalOptions(t *testing.T) {
	t.Parallel()

	t.Run("CORS still works with wildcard origin", func(t *testing.T) {
		middleware := CORS("*", "GET,POST", "Content-Type")

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(
			t,
			"*",
			rec.Header().Get("Access-Control-Allow-Origin"),
			"origin should be wildcard",
		)
		assert.Empty(
			t,
			rec.Header().Get("Access-Control-Allow-Credentials"),
			"credentials should not be set",
		)
	})

	t.Run("CORS still works with specific origin", func(t *testing.T) {
		middleware := CORS("https://example.com", "GET,POST", "Content-Type")

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(
			t,
			"https://example.com",
			rec.Header().Get("Access-Control-Allow-Origin"),
			"origin should be specific",
		)
		assert.Equal(
			t,
			"true",
			rec.Header().Get("Access-Control-Allow-Credentials"),
			"credentials should be set",
		)
	})
}

func TestWithSetHeader(t *testing.T) {
	t.Parallel()

	t.Run("single header set", func(t *testing.T) {
		middleware := NewWithOperations(WithSetHeader("X-Custom", "test-value"))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(t, "test-value", rec.Header().Get("X-Custom"), "header should be set")
	})

	t.Run("multiple WithSetHeader calls for same key", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSetHeader("X-Test", "first"),
			WithSetHeader("X-Test", "second"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Last WithSetHeader call wins (overwrites previous)
		assert.Equal(t, "second", rec.Header().Get("X-Test"), "should use last set value")
		values := rec.Header().Values("X-Test")
		assert.Len(t, values, 1, "should have only one value")
	})

	t.Run("WithSetHeader overwrites existing header", func(t *testing.T) {
		middleware := NewWithOperations(WithSetHeader("Content-Type", "application/json"))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set a different content type
		rec.Header().Set("Content-Type", "text/html")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(
			t,
			"application/json",
			rec.Header().Get("Content-Type"),
			"should overwrite existing header",
		)
	})
}

func TestWithAddHeader(t *testing.T) {
	t.Parallel()

	t.Run("single header add", func(t *testing.T) {
		middleware := NewWithOperations(WithAddHeader("X-Custom", "test-value"))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(t, "test-value", rec.Header().Get("X-Custom"), "header should be added")
	})

	t.Run("WithAddHeader appends to existing header", func(t *testing.T) {
		middleware := NewWithOperations(WithAddHeader("Cache-Control", "no-store"))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set a cache control header
		rec.Header().Set("Cache-Control", "no-cache")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		values := rec.Header().Values("Cache-Control")
		assert.Len(t, values, 2, "should have two Cache-Control values")
		assert.Contains(t, values, "no-cache", "should contain original value")
		assert.Contains(t, values, "no-store", "should contain added value")
	})

	t.Run("multiple WithAddHeader calls for same key", func(t *testing.T) {
		middleware := NewWithOperations(
			WithAddHeader("Set-Cookie", "session=abc123"),
			WithAddHeader("Set-Cookie", "theme=dark"),
			WithAddHeader("Set-Cookie", "lang=en"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		cookies := rec.Header().Values("Set-Cookie")
		assert.Len(t, cookies, 3, "should have three Set-Cookie headers")
		assert.Contains(t, cookies, "session=abc123", "should contain session cookie")
		assert.Contains(t, cookies, "theme=dark", "should contain theme cookie")
		assert.Contains(t, cookies, "lang=en", "should contain lang cookie")
	})
}

func TestMixedOperations(t *testing.T) {
	t.Parallel()

	t.Run("WithSet and WithAdd interaction", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSet(http.Header{"X-Version": []string{"1.0"}}),
			WithAdd(http.Header{"X-Version": []string{"beta"}}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		values := rec.Header().Values("X-Version")
		assert.Len(t, values, 2, "should have two X-Version headers")
		assert.Contains(t, values, "1.0", "should contain set value")
		assert.Contains(t, values, "beta", "should contain added value")
	})

	t.Run("WithSetHeader and WithAddHeader for same key", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSetHeader("X-API", "v1"),
			WithAddHeader("X-API", "experimental"),
			WithAddHeader("X-API", "deprecated"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		values := rec.Header().Values("X-API")
		assert.Len(t, values, 3, "should have three X-API headers")
		assert.Contains(t, values, "v1", "should contain set value")
		assert.Contains(t, values, "experimental", "should contain first added value")
		assert.Contains(t, values, "deprecated", "should contain second added value")
	})

	t.Run("complex operation ordering", func(t *testing.T) {
		middleware := NewWithOperations(
			WithRemove("X-Test"),
			WithSetHeader("X-Test", "set-value"),
			WithAddHeader("X-Test", "add-value1"),
			WithAddHeader("X-Test", "add-value2"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set header that should be removed
		rec.Header().Set("X-Test", "original-value")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		values := rec.Header().Values("X-Test")
		assert.Len(t, values, 3, "should have three X-Test headers")
		assert.Contains(t, values, "set-value", "should contain set value")
		assert.Contains(t, values, "add-value1", "should contain first added value")
		assert.Contains(t, values, "add-value2", "should contain second added value")
		assert.NotContains(t, values, "original-value", "should not contain removed value")
	})
}

func TestEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("no operations", func(t *testing.T) {
		middleware := NewWithOperations()

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		rec.Header().Set("Existing", "value")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(t, "value", rec.Header().Get("Existing"), "existing header should remain")
	})

	t.Run("empty header keys and values", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSetHeader("", "empty-key"),
			WithAddHeader("Empty-Value", ""),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Go's http package handles empty keys/values
		assert.Empty(t, rec.Header().Get("Empty-Value"), "empty value should be set")
	})

	t.Run("case sensitivity", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSetHeader("content-type", "application/json"),
			WithAddHeader("Content-Type", "charset=utf-8"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Go canonicalizes header names
		values := rec.Header().Values("Content-Type")
		assert.Len(t, values, 2, "should have two Content-Type headers")
		assert.Contains(t, values, "application/json", "should contain set value")
		assert.Contains(t, values, "charset=utf-8", "should contain added value")
	})

	t.Run("remove non-canonical header names", func(t *testing.T) {
		middleware := NewWithOperations(WithRemove("content-type", "x-custom-header"))

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Set headers with various capitalizations
		rec.Header().Set("Content-Type", "text/html")
		rec.Header().Set("X-Custom-Header", "value")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Go canonicalizes header names for removal too
		assert.Empty(t, rec.Header().Get("Content-Type"), "Content-Type should be removed")
		assert.Empty(t, rec.Header().Get("X-Custom-Header"), "X-Custom-Header should be removed")
	})
}

func TestRealWorldScenarios(t *testing.T) {
	t.Parallel()

	t.Run("API versioning headers", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSetHeader("X-API-Version", "v2.1"),
			WithAddHeader("X-Supported-Versions", "v1.0"),
			WithAddHeader("X-Supported-Versions", "v2.0"),
			WithAddHeader("X-Supported-Versions", "v2.1"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(t, "v2.1", rec.Header().Get("X-API-Version"), "current version should be set")

		supportedVersions := rec.Header().Values("X-Supported-Versions")
		assert.Len(t, supportedVersions, 3, "should have three supported versions")
		assert.Contains(t, supportedVersions, "v1.0", "should support v1.0")
		assert.Contains(t, supportedVersions, "v2.0", "should support v2.0")
		assert.Contains(t, supportedVersions, "v2.1", "should support v2.1")
	})

	t.Run("security hardening", func(t *testing.T) {
		middleware := NewWithOperations(
			WithRemove("Server", "X-Powered-By", "X-AspNet-Version", "X-AspNetMvc-Version"),
			WithSet(http.Header{
				"X-Content-Type-Options":    []string{"nosniff"},
				"X-Frame-Options":           []string{"DENY"},
				"X-XSS-Protection":          []string{"1; mode=block"},
				"Strict-Transport-Security": []string{"max-age=31536000; includeSubDomains"},
			}),
			WithAddHeader("Content-Security-Policy", "default-src 'self'"),
			WithAddHeader("Content-Security-Policy", "script-src 'self' 'unsafe-inline'"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Simulate server headers that should be removed
		rec.Header().Set("Server", "Apache/2.4.41")
		rec.Header().Set("X-Powered-By", "PHP/7.4")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Verify removals
		assert.Empty(t, rec.Header().Get("Server"), "Server header should be removed")
		assert.Empty(t, rec.Header().Get("X-Powered-By"), "X-Powered-By header should be removed")

		// Verify security headers
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
		assert.Equal(t, "1; mode=block", rec.Header().Get("X-XSS-Protection"))
		assert.Equal(
			t,
			"max-age=31536000; includeSubDomains",
			rec.Header().Get("Strict-Transport-Security"),
		)

		// Verify CSP headers
		cspHeaders := rec.Header().Values("Content-Security-Policy")
		assert.Len(t, cspHeaders, 2, "should have two CSP headers")
		assert.Contains(t, cspHeaders, "default-src 'self'")
		assert.Contains(t, cspHeaders, "script-src 'self' 'unsafe-inline'")
	})

	t.Run("caching strategy", func(t *testing.T) {
		middleware := NewWithOperations(
			WithRemove("ETag", "Last-Modified", "Expires"),
			WithSetHeader("Cache-Control", "no-cache, no-store, must-revalidate"),
			WithAddHeader("Cache-Control", "private"),
			WithSetHeader("Pragma", "no-cache"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Pre-set caching headers that should be removed
		rec.Header().Set("ETag", "\"abc123\"")
		rec.Header().Set("Expires", "Thu, 01 Jan 1970 00:00:00 GMT")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Verify removals
		assert.Empty(t, rec.Header().Get("ETag"), "ETag should be removed")
		assert.Empty(t, rec.Header().Get("Expires"), "Expires should be removed")

		// Verify cache control
		cacheControl := rec.Header().Values("Cache-Control")
		assert.Len(t, cacheControl, 2, "should have two Cache-Control headers")
		assert.Contains(t, cacheControl, "no-cache, no-store, must-revalidate")
		assert.Contains(t, cacheControl, "private")

		assert.Equal(t, "no-cache", rec.Header().Get("Pragma"))
	})

	t.Run("WithSet properly handles multiple Set-Cookie values", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSet(http.Header{
				"Set-Cookie": []string{
					"user=john; Path=/api",
					"token=xyz789; Path=/api; Secure",
				},
			}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		cookies := rec.Header().Values("Set-Cookie")
		assert.Len(t, cookies, 2, "should have two Set-Cookie headers")
		assert.Contains(t, cookies, "user=john; Path=/api")
		assert.Contains(t, cookies, "token=xyz789; Path=/api; Secure")
	})

	t.Run("WithAdd appends to existing Set-Cookie headers", func(t *testing.T) {
		middleware := NewWithOperations(
			WithSet(http.Header{
				"Set-Cookie": []string{"existing=value; Path=/"},
			}),
			WithAdd(http.Header{
				"Set-Cookie": []string{
					"new1=value1; Path=/",
					"new2=value2; Path=/",
				},
			}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		require.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		cookies := rec.Header().Values("Set-Cookie")
		assert.Len(t, cookies, 3, "should have three Set-Cookie headers")
		assert.Contains(t, cookies, "existing=value; Path=/")
		assert.Contains(t, cookies, "new1=value1; Path=/")
		assert.Contains(t, cookies, "new2=value2; Path=/")
	})
}
