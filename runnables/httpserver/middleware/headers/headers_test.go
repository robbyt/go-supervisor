package headers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("sets single header", func(t *testing.T) {
		t.Parallel()

		headers := HeaderMap{
			"X-Test-Header": "test-value",
		}

		middleware := New(headers)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		// Mock handlers
		handlerCalled := false
		handlers := []httpserver.HandlerFunc{
			middleware,
			func(rp *httpserver.RequestProcessor) {
				handlerCalled = true
			},
		}

		// Create route and test
		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, handlers...)
		assert.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.True(t, handlerCalled, "handler should be called")
		assert.Equal(t, "test-value", rec.Header().Get("X-Test-Header"), "header should be set")
	})

	t.Run("sets multiple headers", func(t *testing.T) {
		t.Parallel()

		headers := HeaderMap{
			"X-Header-One": "value-one",
			"X-Header-Two": "value-two",
			"Content-Type": "application/json",
		}

		middleware := New(headers)

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		assert.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(t, "value-one", rec.Header().Get("X-Header-One"), "first header should be set")
		assert.Equal(
			t,
			"value-two",
			rec.Header().Get("X-Header-Two"),
			"second header should be set",
		)
		assert.Equal(
			t,
			"application/json",
			rec.Header().Get("Content-Type"),
			"content type should be set",
		)
	})

	t.Run("handles empty headers map", func(t *testing.T) {
		t.Parallel()

		middleware := New(HeaderMap{})

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		assert.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Should not panic, headers may be present from other middleware
		assert.NotNil(t, rec.Header(), "headers should exist")
	})

	t.Run("allows headers to be overridden by subsequent middleware", func(t *testing.T) {
		t.Parallel()

		headerMiddleware := New(HeaderMap{"X-Test": "original"})
		overrideMiddleware := func(rp *httpserver.RequestProcessor) {
			rp.Writer().Header().Set("X-Test", "overridden")
			rp.Next()
		}

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {},
			headerMiddleware, overrideMiddleware)
		assert.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(t, "overridden", rec.Header().Get("X-Test"), "header should be overridden")
	})
}

func TestJSON(t *testing.T) {
	t.Parallel()

	middleware := JSON()

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
		func(w http.ResponseWriter, r *http.Request) {}, middleware)
	assert.NoError(t, err, "route creation should not fail")

	route.ServeHTTP(rec, req)

	assert.Equal(
		t,
		"application/json",
		rec.Header().Get("Content-Type"),
		"content type should be set to JSON",
	)
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), "cache control should be set")
}

func TestCORS(t *testing.T) {
	t.Parallel()

	t.Run("sets CORS headers with wildcard origin", func(t *testing.T) {
		t.Parallel()

		middleware := CORS("*", "GET,POST,PUT,DELETE", "Content-Type,Authorization")

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		assert.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(
			t,
			"*",
			rec.Header().Get("Access-Control-Allow-Origin"),
			"origin should be wildcard",
		)
		assert.Equal(
			t,
			"GET,POST,PUT,DELETE",
			rec.Header().Get("Access-Control-Allow-Methods"),
			"methods should be set",
		)
		assert.Equal(
			t,
			"Content-Type,Authorization",
			rec.Header().Get("Access-Control-Allow-Headers"),
			"headers should be set",
		)
		assert.Empty(
			t,
			rec.Header().Get("Access-Control-Allow-Credentials"),
			"credentials should not be set for wildcard",
		)
	})

	t.Run("sets CORS headers with specific origin", func(t *testing.T) {
		t.Parallel()

		middleware := CORS("https://example.com", "GET,POST", "Content-Type")

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {}, middleware)
		assert.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		assert.Equal(
			t,
			"https://example.com",
			rec.Header().Get("Access-Control-Allow-Origin"),
			"origin should be specific domain",
		)
		assert.Equal(
			t,
			"GET,POST",
			rec.Header().Get("Access-Control-Allow-Methods"),
			"methods should be set",
		)
		assert.Equal(
			t,
			"Content-Type",
			rec.Header().Get("Access-Control-Allow-Headers"),
			"headers should be set",
		)
		assert.Equal(
			t,
			"true",
			rec.Header().Get("Access-Control-Allow-Credentials"),
			"credentials should be set for specific origin",
		)
	})
}

func TestSecurity(t *testing.T) {
	t.Parallel()

	middleware := Security()

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
		func(w http.ResponseWriter, r *http.Request) {}, middleware)
	assert.NoError(t, err, "route creation should not fail")

	route.ServeHTTP(rec, req)

	assert.Equal(
		t,
		"nosniff",
		rec.Header().Get("X-Content-Type-Options"),
		"content type options should be set",
	)
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"), "frame options should be set")
	assert.Equal(
		t,
		"1; mode=block",
		rec.Header().Get("X-XSS-Protection"),
		"XSS protection should be set",
	)
	assert.Equal(
		t,
		"strict-origin-when-cross-origin",
		rec.Header().Get("Referrer-Policy"),
		"referrer policy should be set",
	)
}

func TestAdd(t *testing.T) {
	t.Parallel()

	middleware := Add("X-Custom-Header", "custom-value")

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
		func(w http.ResponseWriter, r *http.Request) {}, middleware)
	assert.NoError(t, err, "route creation should not fail")

	route.ServeHTTP(rec, req)

	assert.Equal(
		t,
		"custom-value",
		rec.Header().Get("X-Custom-Header"),
		"custom header should be set",
	)
}

func TestHeadersIntegration(t *testing.T) {
	t.Run("multiple header middlewares work together", func(t *testing.T) {
		t.Parallel()

		// Stack multiple header middlewares
		jsonMw := JSON()
		securityMw := Security()
		customMw := Add("X-API-Version", "v1.0")

		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()

		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {},
			jsonMw, securityMw, customMw)
		assert.NoError(t, err, "route creation should not fail")

		route.ServeHTTP(rec, req)

		// Check that all middlewares set their headers
		assert.Equal(
			t,
			"application/json",
			rec.Header().Get("Content-Type"),
			"JSON middleware should set content type",
		)
		assert.Equal(
			t,
			"no-cache",
			rec.Header().Get("Cache-Control"),
			"JSON middleware should set cache control",
		)
		assert.Equal(
			t,
			"nosniff",
			rec.Header().Get("X-Content-Type-Options"),
			"security middleware should set content type options",
		)
		assert.Equal(
			t,
			"DENY",
			rec.Header().Get("X-Frame-Options"),
			"security middleware should set frame options",
		)
		assert.Equal(
			t,
			"v1.0",
			rec.Header().Get("X-API-Version"),
			"custom middleware should set API version",
		)
	})
}
