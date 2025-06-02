package wildcard

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/stretchr/testify/assert"
)

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

// setupRequest creates a basic HTTP request for testing
func setupRequest(t *testing.T, method, path string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	return rec, req
}

// executeHandlerWithWildcard runs the provided handler with the WildcardRouter middleware
func executeHandlerWithWildcard(
	t *testing.T,
	handler http.HandlerFunc,
	prefix string,
	rec *httptest.ResponseRecorder,
	req *http.Request,
) {
	t.Helper()
	// Create a route with wildcard functionality using NewWildcardRoute
	h := func(rp *httpserver.RequestProcessor) {
		handler.ServeHTTP(rp.Writer(), rp.Request())
	}
	route, err := httpserver.NewWildcardRoute(prefix, h)
	assert.NoError(t, err)
	route.ServeHTTP(rec, req)
}

func TestWildcardRouter(t *testing.T) {
	// Create the echo handler once for all tests
	handler := createPathEchoHandler(t)

	t.Run("matches request with correct prefix", func(t *testing.T) {
		// Setup
		rec, req := setupRequest(t, "GET", "/api/users")

		// Execute
		executeHandlerWithWildcard(t, handler, "/api", rec, req)

		// Verify
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "users", rec.Body.String()) // Prefix should be stripped
	})

	t.Run("returns 404 for non-matching prefix", func(t *testing.T) {
		// Setup
		rec, req := setupRequest(t, "GET", "/other/users")

		// Execute
		executeHandlerWithWildcard(t, handler, "/api", rec, req)

		// Verify
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("handles root paths correctly", func(t *testing.T) {
		// Setup
		rec, req := setupRequest(t, "GET", "/api/")

		// Execute
		executeHandlerWithWildcard(t, handler, "/api", rec, req)

		// Verify
		assert.Equal(t, http.StatusOK, rec.Code)
		// The expected path should be empty because stripping "/api/" from "/api/" leaves ""
		assert.Equal(t, "", rec.Body.String())
	})
}
