package headers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestHeaderOperations(t *testing.T) {
	t.Parallel()

	t.Run("WithSetRequest sets request headers", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequest(http.Header{
				"X-Request-ID":     []string{"req-123"},
				"X-Forwarded-Host": []string{"example.com"},
			}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "req-123", capturedHeaders.Get("X-Request-ID"))
		assert.Equal(t, "example.com", capturedHeaders.Get("X-Forwarded-Host"))
	})

	t.Run("WithSetRequestHeader sets single request header", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequestHeader("X-Custom", "test-value"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "test-value", capturedHeaders.Get("X-Custom"))
	})

	t.Run("WithAddRequest adds request headers", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithAddRequest(http.Header{
				"X-Tags": []string{"tag1"},
				"X-Meta": []string{"info"},
			}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Tags", "existing-tag")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		tags := capturedHeaders.Values("X-Tags")
		assert.Len(t, tags, 2)
		assert.Contains(t, tags, "existing-tag")
		assert.Contains(t, tags, "tag1")
		assert.Equal(t, "info", capturedHeaders.Get("X-Meta"))
	})

	t.Run("WithAddRequestHeader adds single request header", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithAddRequestHeader("Accept-Encoding", "gzip"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept-Encoding", "deflate")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		encodings := capturedHeaders.Values("Accept-Encoding")
		assert.Len(t, encodings, 2)
		assert.Contains(t, encodings, "deflate")
		assert.Contains(t, encodings, "gzip")
	})

	t.Run("WithRemoveRequest removes request headers", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithRemoveRequest("X-Forwarded-For", "X-Real-IP"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Forwarded-For", "192.168.1.1")
		req.Header.Set("X-Real-IP", "10.0.0.1")
		req.Header.Set("User-Agent", "test-agent")
		req.Header.Set("Authorization", "Bearer token123")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Empty(t, capturedHeaders.Get("X-Forwarded-For"))
		assert.Empty(t, capturedHeaders.Get("X-Real-IP"))
		assert.Equal(t, "test-agent", capturedHeaders.Get("User-Agent"))
		assert.Equal(t, "Bearer token123", capturedHeaders.Get("Authorization"))
	})

	t.Run("request header operation ordering: remove → set → add", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithRemoveRequest("X-Test"),
			WithSetRequest(http.Header{"X-Test": []string{"set-value"}}),
			WithAddRequest(http.Header{"X-Test": []string{"add-value"}}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Test", "original-value")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		values := capturedHeaders.Values("X-Test")
		assert.Len(t, values, 2)
		assert.Contains(t, values, "set-value")
		assert.Contains(t, values, "add-value")
		assert.NotContains(t, values, "original-value")
	})

	t.Run("multiple WithSetRequestHeader calls for same key", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequestHeader("X-Version", "1.0"),
			WithSetRequestHeader("X-Version", "2.0"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "2.0", capturedHeaders.Get("X-Version"))
		values := capturedHeaders.Values("X-Version")
		assert.Len(t, values, 1)
	})

	t.Run("multiple WithAddRequestHeader calls for same key", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithAddRequestHeader("X-Trace", "trace1"),
			WithAddRequestHeader("X-Trace", "trace2"),
			WithAddRequestHeader("X-Trace", "trace3"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		traces := capturedHeaders.Values("X-Trace")
		assert.Len(t, traces, 3)
		assert.Contains(t, traces, "trace1")
		assert.Contains(t, traces, "trace2")
		assert.Contains(t, traces, "trace3")
	})

	t.Run("WithSetRequestHeader overwrites existing request header", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequestHeader("User-Agent", "custom-agent/1.0"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("User-Agent", "original-agent")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "custom-agent/1.0", capturedHeaders.Get("User-Agent"))
		values := capturedHeaders.Values("User-Agent")
		assert.Len(t, values, 1)
	})

	t.Run("WithAddRequestHeader appends to existing request header", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithAddRequestHeader("Accept", "application/json"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		accepts := capturedHeaders.Values("Accept")
		assert.Len(t, accepts, 2)
		assert.Contains(t, accepts, "text/html")
		assert.Contains(t, accepts, "application/json")
	})

	t.Run("remove non-existent request headers", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithRemoveRequest("Non-Existent-Header"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Existing-Header", "value")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "value", capturedHeaders.Get("Existing-Header"))
		assert.Empty(t, capturedHeaders.Get("Non-Existent-Header"))
	})

	t.Run("empty request header operations", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequest(http.Header{}),
			WithAddRequest(http.Header{}),
			WithRemoveRequest(),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Existing", "value")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "value", capturedHeaders.Get("Existing"))
	})
}

func TestRequestAndResponseHeaderCombinations(t *testing.T) {
	t.Parallel()

	t.Run("request and response headers work together", func(t *testing.T) {
		var capturedRequestHeaders http.Header
		middleware := NewWithOperations(
			// Request operations
			WithRemoveRequest("X-Forwarded-For"),
			WithSetRequest(http.Header{"X-Request-Source": []string{"middleware"}}),
			WithAddRequest(http.Header{"X-Request-Tag": []string{"processed"}}),

			// Response operations
			WithRemove("Server"),
			WithSet(http.Header{"X-API-Version": []string{"v1.0"}}),
			WithAdd(http.Header{"X-Response-Tag": []string{"enhanced"}}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Forwarded-For", "192.168.1.1")
		req.Header.Set("User-Agent", "test-agent")
		rec := httptest.NewRecorder()
		rec.Header().Set("Server", "nginx/1.0")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedRequestHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		// Verify request header modifications
		assert.Empty(t, capturedRequestHeaders.Get("X-Forwarded-For"))
		assert.Equal(t, "test-agent", capturedRequestHeaders.Get("User-Agent"))
		assert.Equal(t, "middleware", capturedRequestHeaders.Get("X-Request-Source"))
		assert.Equal(t, "processed", capturedRequestHeaders.Get("X-Request-Tag"))

		// Verify response header modifications
		assert.Empty(t, rec.Header().Get("Server"))
		assert.Equal(t, "v1.0", rec.Header().Get("X-API-Version"))
		assert.Equal(t, "enhanced", rec.Header().Get("X-Response-Tag"))
	})

	t.Run("same header name in request and response operations", func(t *testing.T) {
		var capturedRequestHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequest(http.Header{"X-Version": []string{"request-v1"}}),
			WithSet(http.Header{"X-Version": []string{"response-v1"}}),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedRequestHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "request-v1", capturedRequestHeaders.Get("X-Version"))
		assert.Equal(t, "response-v1", rec.Header().Get("X-Version"))
	})

	t.Run("complex real-world scenario: proxy header cleanup", func(t *testing.T) {
		var capturedRequestHeaders http.Header
		middleware := NewWithOperations(
			// Remove proxy headers from request
			WithRemoveRequest("X-Forwarded-For", "X-Real-IP", "X-Forwarded-Proto"),
			// Add internal tracking headers to request
			WithSetRequest(http.Header{
				"X-Internal-Request": []string{"true"},
				"X-Request-ID":       []string{"req-12345"},
			}),
			// Add security response headers
			WithSet(http.Header{
				"X-Frame-Options":        []string{"DENY"},
				"X-Content-Type-Options": []string{"nosniff"},
			}),
			// Remove server identification from response
			WithRemove("Server", "X-Powered-By"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Forwarded-For", "192.168.1.1, 10.0.0.1")
		req.Header.Set("X-Real-IP", "192.168.1.1")
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		rec := httptest.NewRecorder()
		rec.Header().Set("Server", "nginx/1.18")
		rec.Header().Set("X-Powered-By", "Express")

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedRequestHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		// Verify proxy headers removed from request
		assert.Empty(t, capturedRequestHeaders.Get("X-Forwarded-For"))
		assert.Empty(t, capturedRequestHeaders.Get("X-Real-IP"))
		assert.Empty(t, capturedRequestHeaders.Get("X-Forwarded-Proto"))

		// Verify internal headers added to request
		assert.Equal(t, "true", capturedRequestHeaders.Get("X-Internal-Request"))
		assert.Equal(t, "req-12345", capturedRequestHeaders.Get("X-Request-ID"))

		// Verify original headers preserved in request
		assert.Equal(t, "Mozilla/5.0", capturedRequestHeaders.Get("User-Agent"))

		// Verify server identification removed from response
		assert.Empty(t, rec.Header().Get("Server"))
		assert.Empty(t, rec.Header().Get("X-Powered-By"))

		// Verify security headers added to response
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	})
}

func TestRequestHeaderEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("empty request header keys and values", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequestHeader("", "empty-key"),
			WithAddRequestHeader("Empty-Value", ""),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "", capturedHeaders.Get("Empty-Value"))
	})

	t.Run("request header case sensitivity", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequestHeader("content-type", "application/json"),
			WithAddRequestHeader("Content-Type", "charset=utf-8"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		values := capturedHeaders.Values("Content-Type")
		assert.Len(t, values, 2)
		assert.Contains(t, values, "application/json")
		assert.Contains(t, values, "charset=utf-8")
	})

	t.Run("remove request headers with non-canonical names", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithRemoveRequest("content-type", "user-agent"),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Content-Type", "text/html")
		req.Header.Set("User-Agent", "test-agent")
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		// Go canonicalizes header names for removal
		assert.Empty(t, capturedHeaders.Get("Content-Type"))
		assert.Empty(t, capturedHeaders.Get("User-Agent"))
		assert.Equal(t, "text/html", capturedHeaders.Get("Accept"))
	})

	t.Run("nil request header maps", func(t *testing.T) {
		var capturedHeaders http.Header
		middleware := NewWithOperations(
			WithSetRequest(nil),
			WithAddRequest(nil),
		)

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Existing", "value")
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
			}, middleware)
		require.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, "value", capturedHeaders.Get("Existing"))
	})
}
