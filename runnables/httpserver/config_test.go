package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testResponseRecorder implements http.ResponseWriter for testing
type testResponseRecorder struct {
	Headers http.Header
	Body    []byte
	Status  int
}

func (r *testResponseRecorder) Header() http.Header {
	return r.Headers
}

func (r *testResponseRecorder) Write(body []byte) (int, error) {
	r.Body = append(r.Body, body...)
	return len(body), nil
}

func (r *testResponseRecorder) WriteHeader(status int) {
	r.Status = status
}

// TestConfigEqual tests the Config.Equal method
func TestConfigEqual(t *testing.T) {
	t.Parallel()

	// Create a common handler for testing
	handler := func(w http.ResponseWriter, r *http.Request) {}

	// Create base config for comparison
	baseRoutes := Routes{
		{
			name:    "v1",
			Path:    "/test",
			Handler: handler,
		},
	}
	baseConfig, err := NewConfig(":8080", baseRoutes,
		WithDrainTimeout(30*time.Second),
		WithReadTimeout(15*time.Second),
		WithWriteTimeout(15*time.Second),
		WithIdleTimeout(1*time.Minute))
	require.NoError(t, err)

	tests := []struct {
		name     string
		config1  *Config
		config2  *Config
		expected bool
	}{
		{
			name:     "Same configs",
			config1:  baseConfig,
			config2:  baseConfig,
			expected: true,
		},
		{
			name:    "Different address",
			config1: baseConfig,
			config2: &Config{
				ListenAddr:   ":9090",
				DrainTimeout: 30 * time.Second,
				ReadTimeout:  15 * time.Second,
				WriteTimeout: 15 * time.Second,
				IdleTimeout:  1 * time.Minute,
				Routes:       baseRoutes,
			},
			expected: false,
		},
		{
			name:    "Different drain timeout",
			config1: baseConfig,
			config2: &Config{
				ListenAddr:   ":8080",
				DrainTimeout: 15 * time.Second,
				ReadTimeout:  15 * time.Second,
				WriteTimeout: 15 * time.Second,
				IdleTimeout:  1 * time.Minute,
				Routes:       baseRoutes,
			},
			expected: false,
		},
		{
			name:    "Different read timeout",
			config1: baseConfig,
			config2: &Config{
				ListenAddr:   ":8080",
				DrainTimeout: 30 * time.Second,
				ReadTimeout:  30 * time.Second, // Different read timeout
				WriteTimeout: 15 * time.Second,
				IdleTimeout:  1 * time.Minute,
				Routes:       baseRoutes,
			},
			expected: false,
		},
		{
			name:    "Different routes",
			config1: baseConfig,
			config2: &Config{
				ListenAddr:   ":8080",
				DrainTimeout: 30 * time.Second,
				ReadTimeout:  15 * time.Second,
				WriteTimeout: 15 * time.Second,
				IdleTimeout:  1 * time.Minute,
				Routes: Routes{
					{
						name:    "v2",
						Path:    "/test2",
						Handler: handler,
					},
				},
			},
			expected: false,
		},
		{
			name:    "Different write timeout",
			config1: baseConfig,
			config2: &Config{
				ListenAddr:   ":8080",
				DrainTimeout: 30 * time.Second,
				ReadTimeout:  15 * time.Second,
				WriteTimeout: 30 * time.Second, // Different write timeout
				IdleTimeout:  1 * time.Minute,
				Routes:       baseRoutes,
			},
			expected: false,
		},
		{
			name:     "One nil config",
			config1:  baseConfig,
			config2:  nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.config1.Equal(tt.config2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestConfigGetMux tests the Config.getMux method
func TestConfigGetMux(t *testing.T) {
	t.Parallel()

	t.Run("Creates ServeMux", func(t *testing.T) {
		var handlerCalled bool
		handler := func(w http.ResponseWriter, r *http.Request) {
			handlerCalled = true
			w.WriteHeader(http.StatusOK)
		}

		routes := Routes{
			{
				name:    "test",
				Path:    "/test",
				Handler: handler,
			},
		}

		config, err := NewConfig(":8080", routes, WithDrainTimeout(30*time.Second))
		require.NoError(t, err)

		// Get mux and create test server
		mux := config.getMux()
		require.NotNil(t, mux, "getMux should return a non-nil ServeMux")

		// Create test server with the mux
		ts := http.NewServeMux()
		ts.Handle("/", mux)

		// Create a test request and response recorder
		req, err := http.NewRequest("GET", "/test", nil)
		require.NoError(t, err)
		rr := &testResponseRecorder{
			Headers: make(http.Header),
			Status:  200,
		}

		// Serve the request
		ts.ServeHTTP(rr, req)

		// Verify handler was called and response is correct
		assert.True(t, handlerCalled, "Handler should have been called")
		assert.Equal(t, http.StatusOK, rr.Status, "Expected status code 200")
	})

	t.Run("Multiple routes added correctly", func(t *testing.T) {
		var handler1Called, handler2Called bool

		handler1 := func(w http.ResponseWriter, r *http.Request) {
			handler1Called = true
			w.WriteHeader(http.StatusOK)
		}

		handler2 := func(w http.ResponseWriter, r *http.Request) {
			handler2Called = true
			w.WriteHeader(http.StatusCreated)
		}

		routes := Routes{
			{name: "route1", Path: "/route1", Handler: handler1},
			{name: "route2", Path: "/route2", Handler: handler2},
		}

		config, err := NewConfig(":8080", routes, WithDrainTimeout(30*time.Second))
		require.NoError(t, err)

		mux := config.getMux()
		require.NotNil(t, mux)

		// Test route 1
		ts := http.NewServeMux()
		ts.Handle("/", mux)

		req1, err := http.NewRequest("GET", "/route1", nil)
		require.NoError(t, err)
		rr1 := &testResponseRecorder{Headers: make(http.Header), Status: 200}
		ts.ServeHTTP(rr1, req1)

		// Test route 2
		req2, err := http.NewRequest("GET", "/route2", nil)
		require.NoError(t, err)
		rr2 := &testResponseRecorder{Headers: make(http.Header), Status: 200}
		ts.ServeHTTP(rr2, req2)

		assert.True(t, handler1Called, "Handler 1 should have been called")
		assert.True(t, handler2Called, "Handler 2 should have been called")
		assert.Equal(t, http.StatusOK, rr1.Status)
		assert.Equal(t, http.StatusCreated, rr2.Status)
	})
}

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
	waitForState(
		t,
		server,
		finitestate.StatusRunning,
		2*time.Second,
		"Server should reach Running state",
	)

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

// TestCreateServer tests that the CreateServer method correctly creates an HTTP server
func TestCreateServer(t *testing.T) {
	t.Parallel()

	// Create a test route
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)
	routes := Routes{*route}

	// Test with default server creator
	config, err := NewConfig(":8080", routes)
	require.NoError(t, err)

	// Create server and verify
	server := config.createServer()
	require.NotNil(t, server, "Server should not be nil")

	// Test with custom server creator
	called := false
	customCreator := func(addr string, handler http.Handler, cfg *Config) HttpServer {
		// Verify parameters
		assert.Equal(t, ":8080", addr, "Address mismatch")
		assert.NotNil(t, handler, "Handler should not be nil")

		// Compare relevant fields individually instead of the whole struct
		assert.Equal(t, config.ListenAddr, cfg.ListenAddr, "ListenAddr mismatch")
		assert.Equal(t, config.DrainTimeout, cfg.DrainTimeout, "DrainTimeout mismatch")
		assert.Equal(t, config.ReadTimeout, cfg.ReadTimeout, "ReadTimeout mismatch")
		assert.Equal(t, config.WriteTimeout, cfg.WriteTimeout, "WriteTimeout mismatch")
		assert.Equal(t, config.IdleTimeout, cfg.IdleTimeout, "IdleTimeout mismatch")
		assert.Equal(
			t,
			fmt.Sprintf("%v", config.Routes),
			fmt.Sprintf("%v", cfg.Routes),
			"Routes mismatch",
		)

		called = true
		return &http.Server{Addr: addr, Handler: handler}
	}

	customConfig, err := NewConfig(":8080", routes, WithServerCreator(customCreator))
	require.NoError(t, err)

	// Create server and verify
	customServer := customConfig.createServer()
	require.NotNil(t, customServer, "Server should not be nil")
	assert.True(t, called, "Custom server creator should be called")

	// Test with nil server creator - should fall back to default
	config.ServerCreator = nil
	server = config.createServer()
	require.NotNil(t, server, "Server should not be nil even with nil ServerCreator")
}

// TestFunctionalOptions tests that all functional options correctly set their respective values
func TestFunctionalOptions(t *testing.T) {
	t.Parallel()

	// Create a basic test route
	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)
	routes := Routes{*route}

	// Define test cases
	tests := []struct {
		name          string
		opts          []ConfigOption
		expectReadTO  time.Duration
		expectWriteTO time.Duration
		expectIdleTO  time.Duration
		expectDrainTO time.Duration
		checkCreator  bool
		checkContext  bool
	}{
		{
			name:          "DefaultTimeouts",
			opts:          nil,
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  false,
			checkContext:  false,
		},
		{
			name:          "CustomDrainTimeout",
			opts:          []ConfigOption{WithDrainTimeout(45 * time.Second)},
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 45 * time.Second, // Custom drain timeout
			checkCreator:  false,
			checkContext:  false,
		},
		{
			name:          "CustomIdleTimeout",
			opts:          []ConfigOption{WithIdleTimeout(3 * time.Minute)},
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  3 * time.Minute,  // Custom idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  false,
			checkContext:  false,
		},
		{
			name:          "CustomReadTimeout",
			opts:          []ConfigOption{WithReadTimeout(45 * time.Second)},
			expectReadTO:  45 * time.Second, // Custom read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  false,
			checkContext:  false,
		},
		{
			name:          "CustomWriteTimeout",
			opts:          []ConfigOption{WithWriteTimeout(45 * time.Second)},
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 45 * time.Second, // Custom write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  false,
			checkContext:  false,
		},
		{
			name: "CustomServerCreator",
			opts: []ConfigOption{
				WithServerCreator(
					func(addr string, handler http.Handler, cfg *Config) HttpServer { return nil },
				),
			},
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  true,
			checkContext:  false,
		},
		{
			name: "MultipleCombinedOptions",
			opts: []ConfigOption{
				WithReadTimeout(20 * time.Second),
				WithWriteTimeout(25 * time.Second),
				WithIdleTimeout(2 * time.Minute),
				WithDrainTimeout(10 * time.Second),
			},
			expectReadTO:  20 * time.Second, // Custom read timeout
			expectWriteTO: 25 * time.Second, // Custom write timeout
			expectIdleTO:  2 * time.Minute,  // Custom idle timeout
			expectDrainTO: 10 * time.Second, // Custom drain timeout
			checkCreator:  false,
			checkContext:  false,
		},
		{
			name:          "NilServerCreator",
			opts:          []ConfigOption{WithServerCreator(nil)},
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  false,            // Should use the default creator
			checkContext:  false,
		},
		{
			name:          "WithRequestContext",
			opts:          []ConfigOption{WithRequestContext(context.Background())},
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  false,
			checkContext:  true,
		},
		{
			name:          "WithRequestContextNil",
			opts:          []ConfigOption{WithRequestContext(context.Background())},
			expectReadTO:  15 * time.Second, // Default read timeout
			expectWriteTO: 15 * time.Second, // Default write timeout
			expectIdleTO:  1 * time.Minute,  // Default idle timeout
			expectDrainTO: 30 * time.Second, // Default drain timeout
			checkCreator:  false,
			checkContext:  true,
		},
	}

	// Run test cases
	for _, tt := range tests {
		tt := tt // Capture range variable
		t.Run(tt.name, func(t *testing.T) {
			config, err := NewConfig(":8080", routes, tt.opts...)
			require.NoError(t, err, "NewConfig should not return an error")
			require.NotNil(t, config, "Config should not be nil")

			// Verify timeouts
			assert.Equal(t, tt.expectReadTO, config.ReadTimeout, "ReadTimeout mismatch")
			assert.Equal(t, tt.expectWriteTO, config.WriteTimeout, "WriteTimeout mismatch")
			assert.Equal(t, tt.expectIdleTO, config.IdleTimeout, "IdleTimeout mismatch")
			assert.Equal(t, tt.expectDrainTO, config.DrainTimeout, "DrainTimeout mismatch")

			// Verify ServerCreator if applicable
			if tt.checkCreator {
				assert.NotNil(t, config.ServerCreator, "ServerCreator should not be nil")
				assert.NotEqual(t,
					fmt.Sprintf("%p", DefaultServerCreator),
					fmt.Sprintf("%p", config.ServerCreator),
					"ServerCreator should not be DefaultServerCreator")
			} else if tt.name == "NilServerCreator" {
				// Special case for nil server creator - should end up using default
				assert.Equal(t,
					fmt.Sprintf("%p", DefaultServerCreator),
					fmt.Sprintf("%p", config.ServerCreator),
					"ServerCreator should be DefaultServerCreator when nil is provided")
			}

			// Verify context if applicable
			if tt.checkContext {
				assert.NotNil(t, config.context, "Context should not be nil when set")
			} else if tt.name == "WithRequestContextNil" {
				assert.NotNil(t, config.context, "Context should not be nil")
			}
		})
	}
}

// TestNewConfig tests that the NewConfig function creates a Config with the expected values
func TestNewConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		addr        string
		routes      Routes
		opts        []ConfigOption
		expectError bool
		expectedStr string
	}{
		{
			name: "ValidConfig",
			addr: ":8080",
			routes: Routes{
				{
					name:    "v1",
					Path:    "/test",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			opts:        []ConfigOption{WithDrainTimeout(30 * time.Second)},
			expectError: false,
			expectedStr: "Config<addr=:8080, drainTimeout=30s, routes=Routes<Name: v1, Path: /test>, timeouts=[read=15s,write=15s,idle=1m0s]>",
		},
		{
			name:        "EmptyRoutes",
			addr:        ":8080",
			routes:      Routes{},
			opts:        nil,
			expectError: true,
			expectedStr: "",
		},
		{
			name: "ZeroDrainTimeout",
			addr: ":8080",
			routes: Routes{
				{
					name:    "v1",
					Path:    "/test",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			opts:        []ConfigOption{WithDrainTimeout(0)},
			expectError: false,
			expectedStr: "Config<addr=:8080, drainTimeout=0s, routes=Routes<Name: v1, Path: /test>, timeouts=[read=15s,write=15s,idle=1m0s]>",
		},
		{
			name: "NegativeDrainTimeout",
			addr: ":8080",
			routes: Routes{
				{
					name:    "v1",
					Path:    "/test",
					Handler: func(w http.ResponseWriter, r *http.Request) {},
				},
			},
			opts:        []ConfigOption{WithDrainTimeout(-10 * time.Second)},
			expectError: false,
			expectedStr: "Config<addr=:8080, drainTimeout=-10s, routes=Routes<Name: v1, Path: /test>, timeouts=[read=15s,write=15s,idle=1m0s]>",
		},
	}

	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			config, err := NewConfig(tt.addr, tt.routes, tt.opts...)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, config)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, config)
				assert.Equal(t, tt.addr, config.ListenAddr)
				// DrainTimeout is now set via options
				assert.Equal(t, tt.routes, config.Routes)
				// Test Config.String()
				assert.Equal(t, tt.expectedStr, config.String())
			}
		})
	}
}

// TestWithConfigCopy tests that the WithConfigCopy option correctly copies settings
func TestWithConfigCopy(t *testing.T) {
	t.Parallel()

	// Create a handler for testing
	handler := func(w http.ResponseWriter, r *http.Request) {}

	// Define a custom context key type to avoid collisions
	type contextKey string
	testKey := contextKey("test")

	// Create a custom context
	ctx := context.WithValue(context.Background(), testKey, "value")

	// Create a custom server creator
	customCreator := func(addr string, handler http.Handler, cfg *Config) HttpServer {
		return &http.Server{Addr: addr, Handler: handler}
	}

	// Create source config with custom settings
	sourceRoutes := Routes{
		{
			name:    "source",
			Path:    "/source",
			Handler: handler,
		},
	}
	sourceConfig, err := NewConfig(":8080", sourceRoutes,
		WithDrainTimeout(45*time.Second),
		WithReadTimeout(30*time.Second),
		WithWriteTimeout(25*time.Second),
		WithIdleTimeout(2*time.Minute),
		WithServerCreator(customCreator),
		WithRequestContext(ctx),
	)
	require.NoError(t, err)

	// Create destination routes
	destRoutes := Routes{
		{
			name:    "dest",
			Path:    "/dest",
			Handler: handler,
		},
	}

	// Test cases
	tests := []struct {
		name           string
		sourceConfig   *Config
		options        []ConfigOption
		expectCopied   bool
		checkNilSource bool
	}{
		{
			name:         "Copy all settings",
			sourceConfig: sourceConfig,
			options:      []ConfigOption{WithConfigCopy(sourceConfig)},
			expectCopied: true,
		},
		{
			name:           "Nil source config",
			sourceConfig:   nil,
			options:        []ConfigOption{WithConfigCopy(nil)},
			expectCopied:   false,
			checkNilSource: true,
		},
		{
			name:         "Copy and override",
			sourceConfig: sourceConfig,
			options: []ConfigOption{
				WithConfigCopy(sourceConfig),
				WithDrainTimeout(10 * time.Second), // Override drain timeout
			},
			expectCopied: true,
		},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a new config using WithConfigCopy
			destConfig, err := NewConfig(":9090", destRoutes, tt.options...)
			require.NoError(t, err)

			// Special case for nil source config
			if tt.checkNilSource {
				// Should have default values
				assert.Equal(t, 30*time.Second, destConfig.DrainTimeout)
				assert.Equal(t, 15*time.Second, destConfig.ReadTimeout)
				assert.Equal(t, 15*time.Second, destConfig.WriteTimeout)
				assert.Equal(t, 1*time.Minute, destConfig.IdleTimeout)
				return
			}

			if tt.expectCopied {
				if len(tt.options) == 1 { // Only WithConfigCopy
					// Check that timeouts were copied
					assert.Equal(t, sourceConfig.DrainTimeout, destConfig.DrainTimeout)
					assert.Equal(t, sourceConfig.ReadTimeout, destConfig.ReadTimeout)
					assert.Equal(t, sourceConfig.WriteTimeout, destConfig.WriteTimeout)
					assert.Equal(t, sourceConfig.IdleTimeout, destConfig.IdleTimeout)

					// Check that ServerCreator was copied
					assert.Equal(t,
						fmt.Sprintf("%p", sourceConfig.ServerCreator),
						fmt.Sprintf("%p", destConfig.ServerCreator))

					// Check that context was copied
					assert.Equal(t, sourceConfig.context, destConfig.context)
				} else { // WithConfigCopy + overrides
					// Check that all values except the overridden one were copied
					assert.NotEqual(t, sourceConfig.DrainTimeout, destConfig.DrainTimeout) // This was overridden
					assert.Equal(t, sourceConfig.ReadTimeout, destConfig.ReadTimeout)
					assert.Equal(t, sourceConfig.WriteTimeout, destConfig.WriteTimeout)
					assert.Equal(t, sourceConfig.IdleTimeout, destConfig.IdleTimeout)
				}

				// Verify that ListenAddr and Routes were NOT copied
				assert.NotEqual(t, sourceConfig.ListenAddr, destConfig.ListenAddr)
				assert.NotEqual(t, sourceConfig.Routes, destConfig.Routes)
			}
		})
	}
}
