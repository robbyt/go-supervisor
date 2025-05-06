package httpserver

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestContextPropagation verifies that the context from the Runner is propagated
// to handlers and that handlers respect context cancellation
func TestContextPropagation(t *testing.T) {
	t.Parallel()

	// Create a channel to signal when the handler receives the request
	handlerStarted := make(chan struct{})
	// Create a channel to signal when the handler completes
	handlerDone := make(chan struct{})
	// Create a channel to signal when the handler detected context cancellation
	contextCanceled := make(chan struct{})

	// Create a handler that blocks until context cancellation
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Signal that the handler has started
		close(handlerStarted)

		// Start a goroutine to respond to context cancellation
		go func() {
			<-r.Context().Done()
			close(contextCanceled)
		}()

		// Block until either we time out (test failure) or the context is canceled
		select {
		case <-r.Context().Done():
			// Context was canceled by server shutdown
			w.WriteHeader(http.StatusServiceUnavailable)
		case <-time.After(10 * time.Second):
			// Test timeout - this is a failure case
			t.Error("Handler did not receive context cancellation in time")
		}

		// Signal that the handler has completed
		close(handlerDone)
	}

	// Create the test route
	route, err := NewRoute("test", "/long-running", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	// Use a unique port for this test
	listenPort := getAvailablePort(t, 9300)

	// Create the server with a short drain timeout
	cfgCallback := func() (*Config, error) {
		return NewConfig(listenPort, hConfig, WithDrainTimeout(2*time.Second))
	}

	// Create a new context that we'll cancel to trigger shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, err := NewRunner(
		WithContext(ctx),
		WithConfigCallback(cfgCallback),
	)
	require.NoError(t, err)

	// Channel to capture Run's completion
	runComplete := make(chan error, 1)

	// Start the server in a goroutine
	go func() {
		err := server.Run(context.Background())
		runComplete <- err
	}()

	// Wait for the server to be ready
	waitForRunningState(t, server, 2*time.Second)

	// Start a client request in a goroutine
	var clientWg sync.WaitGroup
	clientWg.Add(1)
	go func() {
		defer clientWg.Done()
		resp, err := http.Get("http://localhost" + listenPort + "/long-running")
		if err == nil {
			assert.NoError(t, resp.Body.Close())
		}
	}()

	// Wait for the handler to start processing the request
	select {
	case <-handlerStarted:
		// Handler has started processing
	case <-time.After(2 * time.Second):
		t.Fatal("Handler did not start in time")
	}

	// Initiate server shutdown
	cancel() // This should cancel the context passed to the server

	// Verify that the handler's context was canceled
	select {
	case <-contextCanceled:
		// Context was properly propagated and canceled
	case <-time.After(3 * time.Second):
		t.Fatal("Handler did not receive context cancellation")
	}

	// Wait for the handler to complete
	select {
	case <-handlerDone:
		// Handler completed
	case <-time.After(3 * time.Second):
		t.Fatal("Handler did not complete after context cancellation")
	}

	// Wait for the server to shut down
	select {
	case err := <-runComplete:
		assert.NoError(t, err, "Server should shut down without error")
	case <-time.After(5 * time.Second):
		t.Fatal("Server did not shut down in time")
	}

	// Wait for the client request to complete
	clientWg.Wait()
}
