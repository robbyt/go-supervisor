package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReloadContextTreeIsRunCtx verifies that after Reload(callerCtx), the
// replacement server's BaseContext tracks the runner's lifetime — not the
// caller's. Cancelling callerCtx after Reload returns must NOT cancel the
// per-request context of subsequent in-flight requests.
//
// Regression: prior to the channel-dispatch refactor, Reload called
// r.boot(ctx) with the caller's ctx, so the new server's BaseContext fired
// the moment the Reload caller's ctx was cancelled — even though the runner
// itself was still alive. See PR #89 for the same bug class fixed in
// composite.
func TestReloadContextTreeIsRunCtx(t *testing.T) {
	t.Parallel()

	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	// Handler reports whether the per-request context was already cancelled
	// at handler entry. With the fix, this is always false: BaseContext is
	// runCtx and runCtx is still live. Without the fix (caller's ctx leaks
	// into BaseContext), it would be true after callerCancel().
	requestCtxCancelled := make(chan bool, 8)
	handler := func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			requestCtxCancelled <- true
		default:
			requestCtxCancelled <- false
		}
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("OK"))
		assert.NoError(t, err)
	}

	// Each Reload pulls a config with a different drain timeout so the
	// config differs (defeating the ErrOldConfig short-circuit) but the
	// listen addr and route stay stable.
	calls := 0
	cfgCallback := func() (*Config, error) {
		calls++
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		if err != nil {
			return nil, err
		}
		return NewConfig(
			port,
			Routes{*route},
			WithDrainTimeout(time.Duration(calls)*time.Second),
		)
	}

	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	errChan := make(chan error, 1)
	go func() { errChan <- runner.Run(t.Context()) }()
	t.Cleanup(func() {
		runner.Stop()
		require.NoError(t, <-errChan)
	})

	require.Eventually(t, func() bool {
		return runner.GetState() == finitestate.StatusRunning
	}, 2*time.Second, 10*time.Millisecond)

	client := &http.Client{Timeout: 1 * time.Second}

	// Pre-reload sanity: request context is live.
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/", port))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.False(t, <-requestCtxCancelled, "pre-reload request ctx should be live")

	// Reload with a narrower caller ctx; cancel it the moment Reload returns.
	// With the fix, the new server's BaseContext is runCtx (still alive).
	// Without the fix, it would be callerCtx (now cancelled), and the next
	// request's per-request context would be already-cancelled at handler
	// entry.
	callerCtx, callerCancel := context.WithCancel(context.Background())
	runner.Reload(callerCtx)
	require.Equal(t, finitestate.StatusRunning, runner.GetState(),
		"runner should be back in Running after reload")
	callerCancel()

	require.Eventually(t, func() bool {
		resp, err := client.Get(fmt.Sprintf("http://localhost%s/", port))
		if err != nil {
			return false
		}
		assert.NoError(t, resp.Body.Close())
		return true
	}, 2*time.Second, 10*time.Millisecond, "post-reload server should still serve")

	require.False(t, <-requestCtxCancelled,
		"post-reload request ctx must be tied to runCtx, not the cancelled caller ctx")
}

// TestContextValuePropagation verifies that context values are properly propagated from the
// Runner to the HTTP request handlers.
func TestContextValuePropagation(t *testing.T) {
	t.Parallel()

	// Create a parent context with a test value
	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value"
	parentCtx := context.WithValue(t.Context(), testKey, testValue)

	// Create a cancellable context to test cancellation propagation
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// Create a handler that checks the context value
	contextValueReceived := make(chan string, 1)
	contextCancelReceived := make(chan struct{}, 1)

	handler := func(w http.ResponseWriter, r *http.Request) {
		// Check if context value is properly propagated
		if value, ok := r.Context().Value(testKey).(string); ok {
			contextValueReceived <- value
		} else {
			contextValueReceived <- "value-not-found"
		}

		// Also check if cancellation propagates
		go func() {
			<-r.Context().Done()
			contextCancelReceived <- struct{}{}
		}()

		// Send a response
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("OK"))
		assert.NoError(t, err)
	}

	// Create a route with the test handler
	route, err := NewRouteFromHandlerFunc("test", "/test", handler)
	require.NoError(t, err)

	// Get a unique port for this test
	port := fmt.Sprintf(":%d", networking.GetRandomPort(t))

	// Create config with our test context
	cfgCallback := func() (*Config, error) {
		return NewConfig(port, Routes{*route}, WithRequestContext(ctx))
	}

	// Create the runner
	runner, err := NewRunner(
		WithConfigCallback(cfgCallback),
	)
	require.NoError(t, err)

	// Run the server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		err := runner.Run(ctx)
		errChan <- err
	}()

	// Wait for the server to start
	require.Eventually(t, func() bool {
		return runner.GetState() == finitestate.StatusRunning
	}, 2*time.Second, 10*time.Millisecond, "Server should reach Running state")

	// Make a request to the server
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost%s/test", port))
	require.NoError(t, err, "Request to server should succeed")

	// Read and close response body
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "OK", string(body))
	require.NoError(t, resp.Body.Close())

	// Verify the context value was properly received
	select {
	case value := <-contextValueReceived:
		assert.Equal(t, testValue, value, "Context value should be propagated to handlers")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for context value")
	}

	// Cancel the context and check if the cancellation is propagated
	cancel()

	// Wait for cancellation signal
	select {
	case <-contextCancelReceived:
		// Success, cancellation was propagated
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for context cancellation")
	}

	// Server should shut down
	select {
	case err := <-errChan:
		require.NoError(t, err, "Server should shut down cleanly")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for server to shut down")
	}
}
