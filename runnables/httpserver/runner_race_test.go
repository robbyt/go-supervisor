package httpserver

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRunnerRaceConditions verifies that there are no race conditions in the boot and stopServer methods
func TestRunnerRaceConditions(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)

	port := getAvailablePort(t, 8600)

	cfg, err := NewConfig(port, Routes{*route}, WithDrainTimeout(1*time.Second))
	require.NoError(t, err)

	cfgCallback := func() (*Config, error) {
		return cfg, nil
	}

	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	errChan := make(chan error, 1)
	go func() {
		err := runner.Run(context.Background())
		errChan <- err
	}()

	resp, err := http.Get("http://localhost" + port + "/test")
	require.NoError(t, err, "Server should be accepting connections")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close(), "Failed to close response body")

	runner.Stop()

	<-errChan
}

// TestConcurrentReloadsRaceCondition verifies that concurrent reloads don't cause race conditions
func TestConcurrentReloadsRaceCondition(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}

	route, err := NewRoute("test", "/test", handler)
	require.NoError(t, err)

	port := getAvailablePort(t, 8700)

	configVersion := 0
	cfgCallback := func() (*Config, error) {
		configVersion++
		updatedCfg, err := NewConfig(
			port,
			Routes{*route},
			WithDrainTimeout(1*time.Second),
			WithIdleTimeout(time.Duration(configVersion)*time.Millisecond+1*time.Minute),
		)
		return updatedCfg, err
	}

	runner, err := NewRunner(WithConfigCallback(cfgCallback))
	require.NoError(t, err)

	errChan := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		err := runner.Run(ctx)
		errChan <- err
	}()

	for i := 0; i < 5; i++ {
		go func() {
			runner.Reload()
		}()
	}

	time.Sleep(1 * time.Second)

	resp, err := http.Get("http://localhost" + port + "/test")
	require.NoError(t, err, "Server should still be accepting connections after concurrent reloads")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close(), "Failed to close response body")

	cancel()

	<-errChan
}
