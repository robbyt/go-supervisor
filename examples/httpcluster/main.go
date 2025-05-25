package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/robbyt/go-supervisor/internal/networking"
	"github.com/robbyt/go-supervisor/runnables/httpcluster"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware"
	"github.com/robbyt/go-supervisor/supervisor"
)

const (
	// Initial port
	InitialPort = ":8080"

	// Drain timeout for HTTP servers
	DrainTimeout = 2 * time.Second
)

// PortRequest represents the JSON payload for port change requests
type PortRequest struct {
	Port string `json:"port"`
}

// ConfigManager manages dynamic configuration updates for the cluster
type ConfigManager struct {
	cluster     *httpcluster.Runner
	logger      *slog.Logger
	commonMw    []middleware.Middleware
	currentPort string
	mu          sync.RWMutex
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(cluster *httpcluster.Runner, logger *slog.Logger) *ConfigManager {
	return &ConfigManager{
		cluster:     cluster,
		logger:      logger,
		commonMw:    createCommonMiddleware(logger),
		currentPort: InitialPort,
	}
}

// createCommonMiddleware creates the middleware stack used by all routes
func createCommonMiddleware(logger *slog.Logger) []middleware.Middleware {
	return []middleware.Middleware{
		middleware.PanicRecovery(logger.WithGroup("recovery")),
		middleware.Logger(logger.WithGroup("http")),
		middleware.MetricCollector(),
	}
}

// getCurrentPort returns the current port
func (cm *ConfigManager) getCurrentPort() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.currentPort
}

// updatePort updates the cluster configuration with a new port
func (cm *ConfigManager) updatePort(newPort string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Validate port format using the networking package helper
	validatedPort, err := networking.ValidatePort(newPort)
	if err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}

	// Use the validated port for the rest of the function
	newPort = validatedPort

	// Create routes
	statusRoute, err := httpserver.NewRouteWithMiddleware(
		"status",
		"/status",
		cm.createStatusHandler(),
		cm.commonMw...,
	)
	if err != nil {
		return fmt.Errorf("failed to create status route: %w", err)
	}

	configRoute, err := httpserver.NewRouteWithMiddleware(
		"config",
		"/",
		cm.createConfigHandler(),
		cm.commonMw...,
	)
	if err != nil {
		return fmt.Errorf("failed to create config route: %w", err)
	}

	// Create new configuration
	config, err := httpserver.NewConfig(
		newPort,
		httpserver.Routes{*statusRoute, *configRoute},
		httpserver.WithDrainTimeout(DrainTimeout),
	)
	if err != nil {
		return fmt.Errorf("failed to create config: %w", err)
	}

	// Send configuration update (will block until cluster is ready)
	oldPort := cm.currentPort
	cm.cluster.GetConfigSiphon() <- map[string]*httpserver.Config{
		"main": config,
	}
	cm.currentPort = newPort
	cm.logger.Info("Configuration updated", "old_port", oldPort, "new_port", newPort)

	return nil
}

// createStatusHandler creates a handler that reports the current configuration
func (cm *ConfigManager) createStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		currentPort := cm.getCurrentPort()
		response := map[string]string{
			"status": "running",
			"port":   currentPort,
		}

		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(response)
		if err != nil {
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
			return
		}
	}
}

// createConfigHandler creates a handler that accepts port change requests
func (cm *ConfigManager) createConfigHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Show instructions
			fmt.Fprintf(w, "HTTP Cluster Example\n\n")
			fmt.Fprintf(w, "Current port: %s\n\n", cm.getCurrentPort())
			fmt.Fprintf(w, "To change port, send a POST request with JSON:\n")
			fmt.Fprintf(
				w,
				"  curl -X POST http://localhost%s/ -H 'Content-Type: application/json' -d '{\"port\":\":8081\"}'\n",
				cm.getCurrentPort(),
			)
			fmt.Fprintf(w, "\nCheck status:\n")
			fmt.Fprintf(w, "  curl http://localhost%s/status\n", cm.getCurrentPort())

		case http.MethodPost:
			// Handle port change request
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read request body", http.StatusBadRequest)
				return
			}
			defer func() {
				if err := r.Body.Close(); err != nil {
					cm.logger.Error("Failed to close request body", "error", err)
				}
			}()

			var req PortRequest
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, "Invalid JSON format", http.StatusBadRequest)
				return
			}

			if req.Port == "" {
				http.Error(w, "Port field is required", http.StatusBadRequest)
				return
			}

			// Capture old port before updating
			oldPort := cm.getCurrentPort()

			// Update configuration
			if err := cm.updatePort(req.Port); err != nil {
				http.Error(
					w,
					fmt.Sprintf("Failed to update port: %v", err),
					http.StatusInternalServerError,
				)
				return
			}

			// Send success response
			w.Header().Set("Content-Type", "application/json")
			err = json.NewEncoder(w).Encode(map[string]string{
				"status":   "updated",
				"old_port": oldPort,
				"new_port": req.Port,
				"message":  fmt.Sprintf("Server will move to port %s", req.Port),
			})
			if err != nil {
				http.Error(w, "Failed to encode response", http.StatusInternalServerError)
				return
			}

		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// createHTTPCluster initializes the HTTP cluster Runnable
func createHTTPCluster(
	ctx context.Context,
	logHandler slog.Handler,
) (*supervisor.PIDZero, *ConfigManager, error) {
	logger := slog.New(logHandler)

	// Create the httpcluster
	cluster, err := httpcluster.NewRunner(
		httpcluster.WithLogger(logger.WithGroup("httpcluster")),
		httpcluster.WithContext(ctx),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create httpcluster: %w", err)
	}

	// Create the configuration manager
	configMgr := NewConfigManager(cluster, logger)

	// Send initial configuration (channel will block until cluster is ready)
	go func() {
		if err := configMgr.updatePort(InitialPort); err != nil {
			logger.Error("Failed to set initial port", "error", err)
		}
	}()

	// Create supervisor
	sv, err := supervisor.New(
		supervisor.WithContext(ctx),
		supervisor.WithRunnables(cluster),
		supervisor.WithLogHandler(logHandler),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create supervisor: %w", err)
	}
	return sv, configMgr, nil
}

func main() {
	// Configure logger
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		// Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	// Create base context
	ctx := context.Background()

	// Run the cluster
	sv, _, err := createHTTPCluster(ctx, handler)
	if err != nil {
		slog.Error("Failed to setup cluster", "error", err)
		os.Exit(1)
	}

	// Start the supervisor - this will block until receiving an OS signal to shutdown
	slog.Info("Starting supervisor with HTTP cluster",
		"initial_port", InitialPort,
		"instructions", fmt.Sprintf("Visit http://localhost%s for instructions", InitialPort),
	)

	if err := sv.Run(); err != nil {
		slog.Error("Supervisor failed", "error", err)
		os.Exit(1)
	}
}
