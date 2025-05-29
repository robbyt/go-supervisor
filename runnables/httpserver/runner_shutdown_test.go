package httpserver

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopServerFailures(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)

	t.Run("shutdown_timeout", func(t *testing.T) {
		mockServer := &mockHttpServer{
			shutdownFunc: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}

		port := getAvailablePort(t, 8300)
		callback := func() (*Config, error) {
			return NewConfig(port, Routes{*route}, WithDrainTimeout(10*time.Millisecond))
		}

		runner, err := NewRunner(WithConfigCallback(callback))
		require.NoError(t, err)

		runner.server = mockServer

		err = runner.stopServer(context.Background())
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrGracefulShutdownTimeout)
	})

	t.Run("shutdown_error", func(t *testing.T) {
		customErr := errors.New("custom shutdown error")
		mockServer := &mockHttpServer{
			shutdownErr: customErr,
		}

		port := getAvailablePort(t, 8400)
		callback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(callback))
		require.NoError(t, err)

		runner.server = mockServer

		err = runner.stopServer(context.Background())
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrGracefulShutdown)
		assert.Contains(t, err.Error(), customErr.Error())
	})

	t.Run("config_nil_uses_default_timeout", func(t *testing.T) {
		mockServer := &mockHttpServer{}

		port := getAvailablePort(t, 8500)
		callback := func() (*Config, error) {
			return NewConfig(port, Routes{*route})
		}

		runner, err := NewRunner(WithConfigCallback(callback))
		require.NoError(t, err)

		runner.server = mockServer

		// Clear config to test default drain timeout path
		runner.config.Store(nil)

		err = runner.stopServer(context.Background())
		assert.NoError(t, err)
	})
}

type mockHttpServer struct {
	listenAndServeErr error
	shutdownErr       error
	shutdownFunc      func(ctx context.Context) error
}

func (m *mockHttpServer) ListenAndServe() error {
	if m.listenAndServeErr != nil {
		return m.listenAndServeErr
	}
	select {}
}

func (m *mockHttpServer) Shutdown(ctx context.Context) error {
	if m.shutdownFunc != nil {
		return m.shutdownFunc(ctx)
	}
	return m.shutdownErr
}

// mockCountingServer counts the number of times Shutdown is called
type mockCountingServer struct {
	mockHttpServer
	shutdownCount int
	mutex         sync.Mutex
}

func (m *mockCountingServer) Shutdown(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.shutdownCount++
	return m.shutdownErr
}

// TestServerCleanupOnlyOnce tests that the server shutdown is only done once
// even when stopServer is called multiple times.
func TestServerCleanupOnlyOnce(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)

	// Create a mock server that counts shutdown calls
	mockServer := &mockCountingServer{}

	// Set up a config callback
	port := getAvailablePort(t, 8700)
	cfgCallback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	// Create a new runner
	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Replace the server with our mock server
	runner.server = mockServer

	// Run stopServer twice concurrently to verify that sync.Once prevents
	// the actual shutdown from running more than once
	var wg sync.WaitGroup
	wg.Add(2)

	// We need a shared error slice because we'll get an error on the second call
	// when the server is already set to nil
	errorResults := make([]error, 2)

	runStopServer := func(index int) {
		defer wg.Done()
		errorResults[index] = runner.stopServer(context.Background())
	}

	go runStopServer(0)
	go runStopServer(1)

	wg.Wait()

	// Check that shutdown was only called once even though stopServer was called twice
	mockServer.mutex.Lock()
	calls := mockServer.shutdownCount
	mockServer.mutex.Unlock()
	assert.Equal(t, 1, calls, "Shutdown should only be called once")

	// With the current implementation using a separate serverMutex,
	// both calls might succeed since we set server = nil outside sync.Once
	// Verify that the shutdown was only called once
	for _, err := range errorResults {
		// Either err is nil or it's ErrServerNotRunning
		if err != nil {
			assert.ErrorIs(
				t,
				err,
				ErrServerNotRunning,
				"Non-nil errors should be ErrServerNotRunning",
			)
		}
	}
}

// TestServerCleanupResetsOnRestart tests that serverCloseOnce is properly reset
// when the server is restarted, allowing it to be shut down again.
func TestServerCleanupResetsOnRestart(t *testing.T) {
	t.Parallel()

	handler := func(w http.ResponseWriter, r *http.Request) {}
	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)

	// Create a counting server to track shutdowns
	mockServer := &mockCountingServer{}

	// Set up a config callback
	port := getAvailablePort(t, 8800)
	cfgCallback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	// Create a new runner
	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	// Replace the server with our mock server
	runner.server = mockServer

	// Stop the server once
	err = runner.stopServer(context.Background())
	assert.NoError(t, err)

	// Server should be nil after stopServer completes
	assert.Nil(t, runner.server)

	// Simulate a boot sequence that sets a new server and resets serverCloseOnce
	mockServer2 := &mockCountingServer{}
	runner.server = mockServer2
	runner.serverCloseOnce = sync.Once{} // This would normally be done by boot()

	// Attempt to stop the new server
	err = runner.stopServer(context.Background())
	assert.NoError(t, err)

	// The server should be nil again after the second shutdown
	assert.Nil(t, runner.server)

	// Verify second server was also shut down
	mockServer2.mutex.Lock()
	calls := mockServer2.shutdownCount
	mockServer2.mutex.Unlock()
	assert.Equal(t, 1, calls, "Second server should be shut down once")
}
