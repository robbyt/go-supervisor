package httpserver

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBootConfigCreateFailure(t *testing.T) {
	t.Parallel()

	callback := func() (*Config, error) {
		return &Config{
			ListenAddr:    ":8000",
			Routes:        Routes{},
			DrainTimeout:  1 * time.Second,
			ServerCreator: DefaultServerCreator,
		}, nil
	}

	runner, err := NewRunner(WithConfigCallback(callback))
	require.NoError(t, err)

	err = runner.boot()
	require.Error(t, err)
	require.ErrorIs(t, err, ErrCreateConfig)
}

// TestBootFailure tests various boot failure scenarios
func TestBootFailure(t *testing.T) {
	t.Parallel()

	t.Run("Missing config callback", func(t *testing.T) {
		_, err := NewRunner()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "config callback is required")
	})

	t.Run("Config callback returns nil", func(t *testing.T) {
		callback := func() (*Config, error) { return nil, nil }
		runner, err := NewRunner(
			WithConfigCallback(callback),
		)

		require.Error(t, err)
		assert.Nil(t, runner)
		require.ErrorIs(t, err, ErrConfigCallback)
	})

	t.Run("Config callback returns error", func(t *testing.T) {
		callback := func() (*Config, error) { return nil, errors.New("failed to load config") }
		runner, err := NewRunner(
			WithConfigCallback(callback),
		)

		require.Error(t, err)
		assert.Nil(t, runner)
		require.ErrorIs(t, err, ErrConfigCallback)
	})

	t.Run("Server boot fails with invalid port", func(t *testing.T) {
		handler := func(w http.ResponseWriter, r *http.Request) {}
		route, err := NewRouteFromHandlerFunc("v1", "/", handler)
		require.NoError(t, err)

		callback := func() (*Config, error) {
			return &Config{
				ListenAddr:   "invalid-port",
				DrainTimeout: 1 * time.Second,
				Routes:       Routes{*route},
			}, nil
		}

		runner, err := NewRunner(
			WithConfigCallback(callback),
		)

		require.NoError(t, err)
		assert.NotNil(t, runner)

		// Test actual run
		err = runner.Run(t.Context())
		require.Error(t, err)
		// With our readiness probe, the error format is different but should be propagated properly
		require.ErrorIs(t, err, ErrServerBoot)
		assert.Equal(t, finitestate.StatusError, runner.GetState())
	})
}
