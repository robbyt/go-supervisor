package httpserver

import (
	"context"
	"errors"
	"net/http"
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
