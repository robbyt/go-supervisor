package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/robbyt/go-supervisor/runnables/httpcluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunCluster tests that the HTTP cluster can be created and configured
func TestRunCluster(t *testing.T) {
	t.Parallel()

	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})
	ctx := t.Context()

	sv, configMgr, err := RunCluster(ctx, logHandler)
	require.NoError(t, err)
	require.NotNil(t, sv)
	require.NotNil(t, configMgr)

	// Test basic configuration
	assert.Equal(t, InitialPort, configMgr.getCurrentPort())
}

// TestConfigManager tests the configuration manager
func TestConfigManager(t *testing.T) {
	t.Parallel()

	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})
	logger := slog.New(logHandler)

	cluster, err := httpcluster.NewRunner()
	require.NoError(t, err)

	configMgr := NewConfigManager(cluster, logger)

	// Test port tracking
	assert.Equal(t, InitialPort, configMgr.getCurrentPort())
}

// TestPortValidation tests port validation logic
func TestPortValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		port      string
		wantError bool
	}{
		{"valid port", ":8080", false},
		{"valid high port", ":65535", false},
		{"port without colon", "8080", false}, // networking package adds colon automatically
		{"empty port", "", true},
		{"just colon", ":", true},
		{"negative port", ":-1", true},
		{"port too high", ":99999", true},
		{"non-numeric port", ":abc", true},
	}

	for _, tt := range tests {
		tt := tt // Capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Test just the validation logic, not actual server startup
			_, err := networking.ValidatePort(tt.port)
			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestHandlers tests HTTP handlers in isolation
func TestHandlers(t *testing.T) {
	t.Parallel()

	logHandler := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError})
	logger := slog.New(logHandler)

	cluster, err := httpcluster.NewRunner()
	require.NoError(t, err)
	configMgr := NewConfigManager(cluster, logger)

	t.Run("status handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/status", nil)
		w := httptest.NewRecorder()

		handler := configMgr.createStatusHandler()
		handler(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var status map[string]string
		err := json.Unmarshal(w.Body.Bytes(), &status)
		require.NoError(t, err)
		assert.Equal(t, "running", status["status"])
		assert.Equal(t, InitialPort, status["port"])
	})

	t.Run("config handler GET", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		handler := configMgr.createConfigHandler()
		handler(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.Contains(t, body, "HTTP Cluster Example")
		assert.Contains(t, body, "Current port: :8080")
	})

	t.Run("config handler POST invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("invalid json"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler := configMgr.createConfigHandler()
		handler(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("config handler POST missing port", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler := configMgr.createConfigHandler()
		handler(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}
