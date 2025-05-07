package httpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContextValuePropagation verifies that context values are properly propagated from the
// Runner to the HTTP request handlers.
func TestContextValuePropagation(t *testing.T) {
	t.Parallel()

	// Create a parent context with a test value
	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value"
	parentCtx := context.WithValue(context.Background(), testKey, testValue)

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
		require.NoError(t, err)
	}

	// Create a route with the test handler
	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)

	// Get a unique port for this test
	port := getAvailablePort(t, 8400)

	// Create config with our test context
	cfgCallback := func() (*Config, error) {
		return NewConfig(port, Routes{*route}, WithRequestContext(ctx))
	}

	// Create the runner
	runner, err := NewRunner(
		WithContext(ctx),
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
		assert.NoError(t, err, "Server should shut down cleanly")
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for server to shut down")
	}
}
