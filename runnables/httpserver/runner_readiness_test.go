package httpserver

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerReadinessProbe(t *testing.T) {
	t.Parallel()

	route, err := NewRouteFromHandlerFunc(
		"test",
		"/test",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)
	require.NoError(t, err)

	port := getAvailablePort(t, 9100)
	callback := func() (*Config, error) {
		return NewConfig(port, Routes{*route})
	}

	runner, err := NewRunner(WithConfigCallback(callback))
	require.NoError(t, err)

	t.Run("probe_timeout", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()

		err := runner.serverReadinessProbe(ctx, "test.invalid:80")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrServerReadinessTimeout)
	})

	t.Run("successful_probe", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer func() {
			err := listener.Close()
			require.NoError(t, err)
		}()

		_, portStr, err := net.SplitHostPort(listener.Addr().String())
		require.NoError(t, err)
		addr := "127.0.0.1:" + portStr

		go func() {
			conn, err := listener.Accept()
			if err == nil {
				require.NoError(t, conn.Close())
			}
		}()

		err = runner.serverReadinessProbe(context.Background(), addr)
		assert.NoError(t, err)
	})
}
