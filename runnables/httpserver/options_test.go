package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Define a custom type for context keys to avoid string collision
type contextKey string

// TestWithContext verifies the WithContext option works correctly
func TestWithContext(t *testing.T) {
	t.Parallel()

	// Create a custom context with a value using the type-safe key
	testKey := contextKey("test-key")
	customCtx := context.WithValue(context.Background(), testKey, "test-value")

	// Create a server with the custom context
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("v1", "/", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(":0", 1*time.Second, hConfig)
	}

	server, err := NewRunner(WithContext(context.Background()),
		WithConfigCallback(cfgCallback),
		WithContext(customCtx))
	require.NoError(t, err)

	// Verify the custom context was applied
	actualValue := server.ctx.Value(testKey)
	assert.Equal(t, "test-value", actualValue, "Context value should be preserved")

	// Verify cancellation works through server.Stop()
	done := make(chan struct{})
	go func() {
		<-server.ctx.Done()
		close(done)
	}()

	// Call Stop to cancel the internal context
	server.Stop()

	// Wait for the server context to be canceled or timeout
	select {
	case <-done:
		// Success, context was canceled
	case <-time.After(1 * time.Second):
		t.Fatal("Context cancellation not propagated")
	}
}

func TestWithLogHandler(t *testing.T) {
	t.Parallel()

	// Create a custom logger with a buffer for testing output
	var logBuffer strings.Builder
	customHandler := slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug})

	// Create required route and config callback for Runner
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("v1", "/", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	cfgCallback := func() (*Config, error) {
		return NewConfig(":0", 1*time.Second, hConfig)
	}

	// Create a server with the custom logger
	server, err := NewRunner(
		WithContext(context.Background()),
		WithConfigCallback(cfgCallback),
		WithLogHandler(customHandler),
	)
	require.NoError(t, err)

	// Verify the custom logger was applied by checking that it's not the default logger
	assert.NotSame(t, slog.Default(), server.logger, "Server should use custom logger")

	// Log something and check if it appears in our buffer
	server.logger.Info("test message")
	logOutput := logBuffer.String()
	assert.Contains(t, logOutput, "test message", "Logger should write to our buffer")
	assert.Contains(t, logOutput, "httpserver.Runner", "Log should contain the correct group name")
}

func TestWithConfig(t *testing.T) {
	t.Parallel()

	// Create a test server config
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("v1", "/test-route", handler)
	require.NoError(t, err)
	hConfig := Routes{*route}

	testAddr := ":8765" // Use a specific port for identification
	staticConfig, err := NewConfig(testAddr, 2*time.Second, hConfig)
	require.NoError(t, err)

	// Create a server with the static config
	server, err := NewRunner(
		WithContext(context.Background()),
		WithConfig(staticConfig),
	)
	require.NoError(t, err)

	// Verify the config callback was created and returns the correct config
	config, err := server.configCallback()
	require.NoError(t, err)
	assert.NotNil(t, config)

	// Verify config values match what we provided
	assert.Equal(t, testAddr, config.ListenAddr, "Config address should match")
	assert.Equal(t, 2*time.Second, config.DrainTimeout, "Config drain timeout should match")
	assert.Equal(t, "/test-route", config.Routes[0].Path, "Config route path should match")
	assert.Equal(t, "v1", config.Routes[0].name, "Config route name should match")

	// Verify we get the same config instance (not a copy)
	assert.Same(t, staticConfig, config, "Should return the exact same config instance")
}
