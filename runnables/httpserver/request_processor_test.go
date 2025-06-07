package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRequestProcessor_IsAborted(t *testing.T) {
	t.Run("not aborted initially", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: []HandlerFunc{func(*RequestProcessor) {}},
			index:    -1, // Initial state before any processing
		}

		assert.False(t, rp.IsAborted(), "should not be aborted initially")
	})

	t.Run("not aborted during processing", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: []HandlerFunc{func(*RequestProcessor) {}, func(*RequestProcessor) {}},
			index:    0, // Currently processing first handler
		}

		assert.False(t, rp.IsAborted(), "should not be aborted during processing")
	})

	t.Run("aborted after explicit abort", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: []HandlerFunc{func(*RequestProcessor) {}, func(*RequestProcessor) {}},
			index:    0,
		}

		rp.Abort()
		assert.True(t, rp.IsAborted(), "should be aborted after calling Abort()")
	})

	t.Run("aborted when index equals handler count", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		handlers := []HandlerFunc{func(*RequestProcessor) {}, func(*RequestProcessor) {}}
		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: handlers,
			index:    len(handlers), // Index equals handler count
		}

		assert.True(t, rp.IsAborted(), "should be aborted when index equals handler count")
	})

	t.Run("aborted when index exceeds handler count", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		handlers := []HandlerFunc{func(*RequestProcessor) {}}
		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: handlers,
			index:    len(handlers) + 1, // Index exceeds handler count
		}

		assert.True(t, rp.IsAborted(), "should be aborted when index exceeds handler count")
	})

	t.Run("aborted with empty handler list", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: []HandlerFunc{}, // Empty handler list
			index:    0,
		}

		assert.True(t, rp.IsAborted(), "should be aborted with empty handler list")
	})
}

func TestRequestProcessor_Abort(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	rw := newResponseWriter(httptest.NewRecorder())

	handlers := []HandlerFunc{func(*RequestProcessor) {}, func(*RequestProcessor) {}}
	rp := &RequestProcessor{
		writer:   rw,
		request:  req,
		handlers: handlers,
		index:    0,
	}

	assert.False(t, rp.IsAborted(), "should not be aborted initially")

	rp.Abort()

	assert.True(t, rp.IsAborted(), "should be aborted after calling Abort()")
	assert.Equal(t, len(handlers), rp.index, "index should be set to handler count")
}

func TestRequestProcessor_Next(t *testing.T) {
	t.Run("executes handlers in sequence", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		var executionOrder []int

		handlers := []HandlerFunc{
			func(rp *RequestProcessor) {
				executionOrder = append(executionOrder, 1)
				rp.Next()
			},
			func(rp *RequestProcessor) {
				executionOrder = append(executionOrder, 2)
				rp.Next()
			},
			func(rp *RequestProcessor) {
				executionOrder = append(executionOrder, 3)
			},
		}

		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: handlers,
			index:    -1,
		}

		rp.Next()

		assert.Equal(t, []int{1, 2, 3}, executionOrder, "handlers should execute in sequence")
		assert.True(t, rp.IsAborted(), "should be aborted after all handlers complete")
	})

	t.Run("stops execution after abort", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		var executionOrder []int

		handlers := []HandlerFunc{
			func(rp *RequestProcessor) {
				executionOrder = append(executionOrder, 1)
				rp.Next()
			},
			func(rp *RequestProcessor) {
				executionOrder = append(executionOrder, 2)
				rp.Abort() // Abort here
			},
			func(rp *RequestProcessor) {
				executionOrder = append(executionOrder, 3) // Should not execute
			},
		}

		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: handlers,
			index:    -1,
		}

		rp.Next()

		assert.Equal(t, []int{1, 2}, executionOrder, "should stop execution after abort")
		assert.True(t, rp.IsAborted(), "should be aborted after calling Abort()")
	})

	t.Run("handles empty handler list", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		rw := newResponseWriter(httptest.NewRecorder())

		rp := &RequestProcessor{
			writer:   rw,
			request:  req,
			handlers: []HandlerFunc{},
			index:    -1,
		}

		rp.Next() // Should not panic

		assert.True(t, rp.IsAborted(), "should be aborted with empty handler list")
	})
}

func TestRequestProcessor_Writer(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	rw := newResponseWriter(httptest.NewRecorder())

	rp := &RequestProcessor{
		writer:  rw,
		request: req,
	}

	assert.Equal(t, rw, rp.Writer(), "should return the same ResponseWriter instance")
}

func TestRequestProcessor_Request(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	rw := newResponseWriter(httptest.NewRecorder())

	rp := &RequestProcessor{
		writer:  rw,
		request: req,
	}

	assert.Equal(t, req, rp.Request(), "should return the same HTTP request instance")
}

func TestRequestProcessor_IntegrationWithRoute(t *testing.T) {
	t.Run("middleware chain with abort", func(t *testing.T) {
		var executionOrder []string
		var isAbortedStates []bool

		middleware1 := func(rp *RequestProcessor) {
			executionOrder = append(executionOrder, "middleware1-start")
			isAbortedStates = append(isAbortedStates, rp.IsAborted())
			rp.Next()
			executionOrder = append(executionOrder, "middleware1-end")
			isAbortedStates = append(isAbortedStates, rp.IsAborted())
		}

		middleware2 := func(rp *RequestProcessor) {
			executionOrder = append(executionOrder, "middleware2-start")
			isAbortedStates = append(isAbortedStates, rp.IsAborted())
			rp.Abort() // Abort in middleware2
			executionOrder = append(executionOrder, "middleware2-abort")
			isAbortedStates = append(isAbortedStates, rp.IsAborted())
		}

		handler := func(rp *RequestProcessor) {
			executionOrder = append(executionOrder, "handler") // Should not execute
			isAbortedStates = append(isAbortedStates, rp.IsAborted())
		}

		route, err := NewRouteFromHandlerFunc("test", "/test",
			func(w http.ResponseWriter, r *http.Request) {
				handler(&RequestProcessor{
					writer:  newResponseWriter(w),
					request: r,
				})
			},
			middleware1, middleware2)

		assert.NoError(t, err, "route creation should not fail")

		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)

		route.ServeHTTP(rec, req)

		expectedOrder := []string{
			"middleware1-start",
			"middleware2-start",
			"middleware2-abort",
			"middleware1-end",
		}
		assert.Equal(
			t,
			expectedOrder,
			executionOrder,
			"execution should stop after abort but allow middleware cleanup",
		)

		expectedAbortedStates := []bool{
			false, // middleware1-start: not aborted yet
			false, // middleware2-start: not aborted yet
			true,  // middleware2-abort: aborted after Abort() call
			true,  // middleware1-end: still aborted
		}
		assert.Equal(
			t,
			expectedAbortedStates,
			isAbortedStates,
			"IsAborted should reflect correct state throughout execution",
		)
	})
}

func TestRequestProcessor_SetWriter(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	originalRW := newResponseWriter(httptest.NewRecorder())
	newRW := newResponseWriter(httptest.NewRecorder())

	rp := &RequestProcessor{
		writer:  originalRW,
		request: req,
	}

	// Verify original writer
	assert.Same(t, originalRW, rp.Writer(), "should initially have original writer")

	// Set new writer
	rp.SetWriter(newRW)

	// Verify new writer is set
	assert.Same(t, newRW, rp.Writer(), "should have new writer after SetWriter")
}
