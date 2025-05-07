package httpserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// We use the waitForRunningState function defined in runner_test.go

// TestListenerReuse verifies that listeners are reused across server reloads
func TestListenerReuse(t *testing.T) {
	t.Parallel()

	// Create test routes with a unique response for identification
	route, err := NewRoute("v1", "/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("TestListenerReuse handler"))
		require.NoError(t, err)
	})
	require.NoError(t, err)
	routes := Routes{*route}

	initialConfig := func() (*Config, error) {
		return NewConfig(
			"127.0.0.1:0", // Use port 0 for automatic port assignment
			routes,
			WithReuseListener(true),
		)
	}

	// Create a runner with a background context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner, err := NewRunner(
		WithContext(ctx),
		WithConfigCallback(initialConfig),
	)
	require.NoError(t, err)

	// Setup done channel to track when Run exits
	done := make(chan struct{})

	// Start the server
	go func() {
		err := runner.Run(ctx)
		require.NoError(t, err)
		close(done)
	}()

	// Wait for the server to be in the Running state
	waitForRunningState(t, runner, 5*time.Second, "Server should reach Running state")

	// Now we should have a listener
	require.NotNil(t, runner.listener, "Listener should be created")

	// Capture the original listener for later comparison
	originalListener := runner.listener

	// Get the listener address for later comparison
	originalAddr := runner.listener.Addr().String()
	require.NotEmpty(t, originalAddr, "Should have a valid listener address")
	t.Logf("Original listener using address: %s", originalAddr)

	// Make an HTTP request to confirm the server is working
	url := fmt.Sprintf("http://%s/test", originalAddr)
	resp, err := http.Get(url)
	require.NoError(t, err, "HTTP request should succeed")
	require.Equal(t, http.StatusOK, resp.StatusCode, "HTTP response should be 200 OK")

	// Read and verify the response body
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Should be able to read response body")
	assert.NoError(t, resp.Body.Close(), "Should be able to close response body")
	require.Equal(t, "TestListenerReuse handler", string(body), "Response body should match")

	// Reload the server with new configuration
	runner.Reload()

	// Wait for the server to reach Running state again
	waitForRunningState(t, runner, 5*time.Second, "Server should reach Running state after reload")

	// Verify the listener is the same object (was reused)
	require.NotNil(t, runner.listener, "Listener should still exist after reload")
	reloadedAddr := runner.listener.Addr().String()
	assert.Equal(t, originalAddr, reloadedAddr, "Listener address should be the same after reload")

	// Verify that the listener instance is exactly the same (not just a new listener with same address)
	assert.Equal(t, fmt.Sprintf("%p", originalListener), fmt.Sprintf("%p", runner.listener),
		"The listener instance should be the same object when ReuseListener is true")

	// Make another HTTP request to confirm the server is still working after reload
	resp2, err := http.Get(url)
	require.NoError(t, err, "HTTP request after reload should succeed")
	require.Equal(t, http.StatusOK, resp2.StatusCode, "HTTP response after reload should be 200 OK")

	// Read and verify the response body after reload
	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err, "Should be able to read response body after reload")
	assert.NoError(t, resp2.Body.Close(), "Should be able to close response body after reload")
	require.Equal(
		t,
		"TestListenerReuse handler",
		string(body2),
		"Response body after reload should match",
	)

	// Shutdown the server
	cancel()

	// Wait for Run to complete
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		require.Fail(t, "Runner.Run() did not exit in time")
	}
}

// TestListenerReplacement verifies that when ReuseListener is false, a new listener is created
func TestListenerReplacement(t *testing.T) {
	t.Parallel()

	// Create test routes with unique responses for identification
	route, err := NewRoute("v1", "/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("TestListenerReplacement handler"))
		require.NoError(t, err)
	})
	require.NoError(t, err)
	routes := Routes{*route}

	// First config with listener reuse enabled
	initialConfig := func() (*Config, error) {
		return NewConfig(
			"127.0.0.1:0", // Use port 0 for automatic port assignment
			routes,
			WithReuseListener(true), // Initially, set to reuse
		)
	}

	// Second config with listener reuse disabled to force creation of a new listener
	updatedConfig := func() (*Config, error) {
		return NewConfig(
			"127.0.0.1:0", // Same pattern, but we'll disable reuse
			routes,
			WithReuseListener(false), // Change to not reuse
		)
	}

	var configCallback ConfigCallback = initialConfig

	// Create a runner with a background context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create a runner that can have its config function swapped
	runner, err := NewRunner(
		WithContext(ctx),
		WithConfigCallback(func() (*Config, error) {
			return configCallback()
		}),
	)
	require.NoError(t, err)

	// Setup done channel to track when Run exits
	done := make(chan struct{})

	// Start the server
	go func() {
		err := runner.Run(ctx)
		require.NoError(t, err)
		close(done)
	}()

	// Wait for the server to reach Running state
	waitForRunningState(t, runner, 5*time.Second, "Server should reach Running state")

	// Now we should have a listener
	require.NotNil(t, runner.listener, "Listener should be created")

	// Store a reference to the original listener and its address
	originalListener := runner.listener
	originalAddr := runner.listener.Addr().String()
	originalPointer := fmt.Sprintf("%p", originalListener)
	t.Logf("Original listener using address: %s", originalAddr)

	// Make an HTTP request to confirm the server is working
	originalURL := fmt.Sprintf("http://%s/test", originalAddr)
	resp, err := http.Get(originalURL)
	require.NoError(t, err, "HTTP request should succeed")
	require.Equal(t, http.StatusOK, resp.StatusCode, "HTTP response should be 200 OK")

	// Read and verify the response body
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Should be able to read response body")
	assert.NoError(t, resp.Body.Close(), "Should be able to close response body")
	require.Equal(t, "TestListenerReplacement handler", string(body), "Response body should match")

	// Swap the config callback to use the non-reusing config
	configCallback = updatedConfig

	// Reload the server with new configuration
	runner.Reload()

	// Wait for the server to reach Running state again
	waitForRunningState(t, runner, 5*time.Second, "Server should reach Running state after reload")

	// Verify we have a listener after reload
	require.NotNil(t, runner.listener, "Listener should exist after reload")

	// Get address and pointer of the new listener
	reloadedAddr := runner.listener.Addr().String()
	reloadedPointer := fmt.Sprintf("%p", runner.listener)
	t.Logf("Reloaded listener using address: %s", reloadedAddr)

	// Check if we have a different listener instance
	assert.NotEqual(t, originalPointer, reloadedPointer,
		"Should have a different listener instance when ReuseListener is false")

	// When using dynamic port assignment (port 0), we also expect a different port
	if strings.Contains(originalAddr, ":0") || strings.Contains(reloadedAddr, ":0") {
		_, origPort, err := net.SplitHostPort(originalAddr)
		require.NoError(t, err)
		_, newPort, err := net.SplitHostPort(reloadedAddr)
		require.NoError(t, err)

		// Only check different ports if we were actually assigned random ports
		if origPort != "0" && newPort != "0" {
			assert.NotEqual(
				t,
				origPort,
				newPort,
				"Should have a different port when creating a new listener with dynamic port assignment",
			)
		}
	}

	// Try the original URL - it should fail because we're using a new listener with a different port
	//nolint:bodyclose
	resp1, err := http.Get(originalURL)
	assert.Error(t, err, "HTTP request to original URL should fail")
	assert.Nil(t, resp1)

	// Now try with the new URL
	newURL := fmt.Sprintf("http://%s/test", reloadedAddr)
	resp2, err := http.Get(newURL)
	require.NoError(t, err, "HTTP request to new URL should succeed")
	require.Equal(t, http.StatusOK, resp2.StatusCode, "HTTP response should be 200 OK")

	// Read and verify the response body
	body2, err := io.ReadAll(resp2.Body)
	require.NoError(t, err, "Should be able to read response body")
	assert.NoError(t, resp2.Body.Close(), "Should be able to close response body")
	require.Equal(t, "TestListenerReplacement handler", string(body2), "Response body should match")

	// Shutdown the server
	cancel()

	// Wait for Run to complete
	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		require.Fail(t, "Runner.Run() did not exit in time")
	}
}
