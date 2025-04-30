package httpserver

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Simple ResponseRecorder for testing
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
	baseConfig, err := NewConfig(":8080", baseRoutes, WithDrainTimeout(30*time.Second))
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

	t.Run("Creates proper ServeMux", func(t *testing.T) {
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
