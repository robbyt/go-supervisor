// Package middleware_test provides compliance testing for httpserver middleware.
//
// This test suite validates that middleware implementations follow the documented
// guidelines in the httpserver README. The compliance tests ensure middleware:
//
// 1. Call either rp.Next() or rp.Abort() (never neither, never both simultaneously)
// 2. Don't panic under normal conditions
// 3. Handle pre-aborted chains gracefully
// 4. Preserve request/response objects without corruption
//
// These tests validate the core contract that all middleware must follow to work
// correctly within the httpserver middleware chain.
//
// # Adding New Middleware to Compliance Tests
//
// To test a new middleware for compliance:
//
//	func TestMyMiddlewareCompliance(t *testing.T) {
//	    myMiddleware := mymiddleware.New(/* any required parameters */)
//	    test := NewMiddlewareComplianceTest(t, "my-middleware", myMiddleware)
//	    test.RunAllTests()
//	}
//
// For middleware with special configuration needs, see the existing examples
// in TestBuiltinMiddlewareCompliance() for patterns.
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/headers"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/logger"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/metrics"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/recovery"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/state"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/wildcard"
	"github.com/stretchr/testify/assert"
)

// MiddlewareComplianceTest validates that middleware follows the documented guidelines
type MiddlewareComplianceTest struct {
	name       string
	middleware httpserver.HandlerFunc
	t          *testing.T
}

// NewMiddlewareComplianceTest creates a new compliance test for the given middleware
func NewMiddlewareComplianceTest(
	t *testing.T,
	name string,
	middleware httpserver.HandlerFunc,
) *MiddlewareComplianceTest {
	t.Helper()
	return &MiddlewareComplianceTest{
		name:       name,
		middleware: middleware,
		t:          t,
	}
}

// RunAllTests runs all compliance tests
func (mct *MiddlewareComplianceTest) RunAllTests() {
	mct.t.Run(mct.name+" compliance", func(t *testing.T) {
		mct.testCallsNextOrAbort()
		mct.testDoesNotPanic()
		mct.testHandlesAbortedChain()
		mct.testPreservesRequestResponse()
	})
}

// testCallsNextOrAbort verifies middleware calls either Next() or Abort()
func (mct *MiddlewareComplianceTest) testCallsNextOrAbort() {
	mct.t.Run("calls Next() or Abort()", func(t *testing.T) {
		nextCalled := false

		// Mock handler that tracks if it was called
		mockHandler := func(rp *httpserver.RequestProcessor) {
			nextCalled = true
		}

		// Create request processor with our tracking
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		rp := createRequestProcessor(
			t,
			rec,
			req,
			[]httpserver.HandlerFunc{mct.middleware, mockHandler},
		)

		// We'll check if the chain continued or was aborted

		// Execute the middleware
		rp.Next()

		// Check if processing continued (Next was called) or was aborted
		if !nextCalled && !rp.IsAborted() {
			t.Error(
				"Middleware must call either Next() or Abort(), but processing did not continue and chain was not aborted",
			)
		}
	})
}

// testDoesNotPanic verifies middleware doesn't panic under normal conditions
func (mct *MiddlewareComplianceTest) testDoesNotPanic() {
	mct.t.Run("does not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Middleware panicked: %v", r)
			}
		}()

		// Create a normal request processor
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		rp := createRequestProcessor(
			t,
			rec,
			req,
			[]httpserver.HandlerFunc{mct.middleware, func(rp *httpserver.RequestProcessor) {}},
		)

		// Execute middleware
		rp.Next()
	})
}

// testHandlesAbortedChain verifies middleware handles pre-aborted chains gracefully
func (mct *MiddlewareComplianceTest) testHandlesAbortedChain() {
	mct.t.Run("handles aborted chain", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		rp := createRequestProcessor(t, rec, req, []httpserver.HandlerFunc{mct.middleware})

		// Pre-abort the chain
		rp.Abort()

		// Middleware should handle this gracefully
		assert.NotPanics(mct.t, func() {
			mct.middleware(rp)
		})
	})
}

// testPreservesRequestResponse verifies middleware doesn't corrupt request/response
func (mct *MiddlewareComplianceTest) testPreservesRequestResponse() {
	mct.t.Run("preserves request/response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Test-Header", "test-value")
		rec := httptest.NewRecorder()

		rp := createRequestProcessor(
			t,
			rec,
			req,
			[]httpserver.HandlerFunc{mct.middleware, func(rp *httpserver.RequestProcessor) {}},
		)

		// Execute middleware
		rp.Next()

		// Verify request and writer are still accessible and unchanged
		assert.Equal(t, req, rp.Request(), "Request should be preserved")
		assert.NotNil(t, rp.Writer(), "Writer should be preserved")
		assert.Equal(
			t,
			"test-value",
			rp.Request().Header.Get("Test-Header"),
			"Request headers should be preserved",
		)
	})
}

// createRequestProcessor is a helper to create a RequestProcessor (using Route to access the internal constructor)
func createRequestProcessor(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	req *http.Request,
	handlers []httpserver.HandlerFunc,
) *httpserver.RequestProcessor {
	t.Helper()
	// Since we can't access the constructor directly, we'll create a route and extract the processor
	// This is a bit hacky but necessary since RequestProcessor constructor is not exported
	var capturedRP *httpserver.RequestProcessor
	wrapperHandler := func(rp *httpserver.RequestProcessor) {
		capturedRP = rp
		// Don't call Next here, we want to capture the processor before execution
	}

	// Add our wrapper as the first handler, then the test handlers
	allHandlers := append([]httpserver.HandlerFunc{wrapperHandler}, handlers...)
	wrapperRoute, err := httpserver.NewRouteFromHandlerFunc(
		"wrapper",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {},
		allHandlers...)
	if err != nil {
		t.Fatalf("Failed to create wrapper route: %v", err)
	}
	wrapperRoute.ServeHTTP(rec, req)

	return capturedRP
}

// Test all built-in middleware for compliance
func TestBuiltinMiddlewareCompliance(t *testing.T) {
	// Test headers middleware
	t.Run("headers middleware", func(t *testing.T) {
		headersMiddleware := headers.New(headers.HeaderMap{"Content-Type": "application/json"})
		test := NewMiddlewareComplianceTest(t, "headers", headersMiddleware)
		test.RunAllTests()
	})

	// Test logger middleware
	t.Run("logger middleware", func(t *testing.T) {
		loggerMiddleware := logger.New(nil) // nil handler for silent logging
		test := NewMiddlewareComplianceTest(t, "logger", loggerMiddleware)
		test.RunAllTests()
	})

	// Test metrics middleware
	t.Run("metrics middleware", func(t *testing.T) {
		metricsMiddleware := metrics.New()
		test := NewMiddlewareComplianceTest(t, "metrics", metricsMiddleware)
		test.RunAllTests()
	})

	// Test recovery middleware
	t.Run("recovery middleware", func(t *testing.T) {
		recoveryMiddleware := recovery.New(nil) // nil handler for silent recovery
		test := NewMiddlewareComplianceTest(t, "recovery", recoveryMiddleware)
		test.RunAllTests()
	})

	// Test state middleware
	t.Run("state middleware", func(t *testing.T) {
		stateMiddleware := state.New(func() string { return "running" })
		test := NewMiddlewareComplianceTest(t, "state", stateMiddleware)
		test.RunAllTests()
	})

	// Test wildcard middleware
	t.Run("wildcard middleware", func(t *testing.T) {
		wildcardMiddleware := wildcard.New("/api/")
		test := NewMiddlewareComplianceTest(t, "wildcard", wildcardMiddleware)
		test.RunAllTests()
	})
}

// Example test for custom middleware compliance
func TestCustomMiddlewareCompliance(t *testing.T) {
	// Test a simple middleware that just calls Next()
	simpleMiddleware := func(rp *httpserver.RequestProcessor) {
		rp.Next()
	}

	test := NewMiddlewareComplianceTest(t, "simple", simpleMiddleware)
	test.RunAllTests()
}

// Example test for middleware that aborts
func TestAbortingMiddlewareCompliance(t *testing.T) {
	// Test middleware that aborts on certain conditions
	abortingMiddleware := func(rp *httpserver.RequestProcessor) {
		if rp.Request().Header.Get("Abort") == "true" {
			http.Error(rp.Writer(), "Aborted", http.StatusForbidden)
			rp.Abort()
			return
		}
		rp.Next()
	}

	test := NewMiddlewareComplianceTest(t, "aborting", abortingMiddleware)
	test.RunAllTests()

	// Additional test for abort behavior
	t.Run("aborts when header present", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Abort", "true")
		rec := httptest.NewRecorder()

		// Create route to test aborting behavior
		route, err := httpserver.NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {},
			abortingMiddleware,
		)
		assert.NoError(t, err)

		route.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusForbidden, rec.Code, "Should return forbidden status")
		assert.Contains(t, rec.Body.String(), "Aborted", "Should contain abort message")
	})
}
