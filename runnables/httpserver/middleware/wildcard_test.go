package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

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

// executeHandlerWithWildcard runs the provided handler with the WildcardRouter middleware
func executeHandlerWithWildcard(
	t *testing.T,
	handler http.HandlerFunc,
	prefix string,
	rec *httptest.ResponseRecorder,
	req *http.Request,
) {
	t.Helper()
	wrappedHandler := WildcardRouter(prefix)(handler)
	wrappedHandler(rec, req)
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
		assert.Equal(t, "/users", rec.Body.String()) // Prefix should be stripped
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
		rec, req := setupRequest(t, "GET", "/api")

		// Execute
		executeHandlerWithWildcard(t, handler, "/api", rec, req)

		// Verify
		assert.Equal(t, http.StatusOK, rec.Code)
		// The expected path can be empty because StripPrefix with an exact match
		// can result in an empty path, which http.ServeMux internally
		// treats as "/"
		assert.Equal(t, "", rec.Body.String())
	})
}
