// Package main demonstrates request and response header manipulation using
// go-supervisor's headers middleware.
//
// # Header Manipulation Example
//
// This example shows how to use the new request header manipulation features
// alongside existing response header functionality:
//
// 1. Request Header Operations:
//   - Remove potentially sensitive headers (X-Forwarded-For)
//   - Add custom request headers (X-Internal-Request)
//   - Set specific request headers (X-Request-Source)
//
// 2. Response Header Operations:
//   - Add security headers (X-Frame-Options)
//   - Set custom API headers (X-API-Version)
//   - Remove server identification headers (Server)
//
// 3. Header Inspection Route:
//   - Displays all request headers received by the handler
//   - Shows how request headers are modified before reaching handlers
//   - Demonstrates response headers are added after handler execution
//
// The middleware processes headers in this order:
// Request: remove → set → add (before calling handler)
// Response: remove → set → add (after handler returns)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/headers"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/logger"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/metrics"
	"github.com/robbyt/go-supervisor/runnables/httpserver/middleware/recovery"
	"github.com/robbyt/go-supervisor/supervisor"
)

const (
	// Port the HTTP server binds to
	ListenOn = ":8082"

	// How long the supervisor waits for the HTTP server to drain before forcefully shutting down
	DrainTimeout = 5 * time.Second
)

// HeaderInfo represents header information for JSON response
type HeaderInfo struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

// HeaderResponse represents the complete header inspection response
type HeaderResponse struct {
	Message         string       `json:"message"`
	RequestHeaders  []HeaderInfo `json:"request_headers"`
	ResponseHeaders []HeaderInfo `json:"response_headers,omitempty"`
	Timestamp       string       `json:"timestamp"`
}

// buildRoutes sets up HTTP routes demonstrating header manipulation
func buildRoutes(logHandler slog.Handler) ([]httpserver.Route, error) {
	// Create base middleware stack
	recoveryMw := recovery.New(logHandler.WithGroup("recovery"))
	loggingMw := logger.New(logHandler.WithGroup("headers_example"))
	metricsMw := metrics.New()

	// Create comprehensive headers middleware that demonstrates both
	// request and response header manipulation
	headersMw := headers.NewWithOperations(
		// Request header operations (applied before handler)
		headers.WithRemoveRequest("X-Forwarded-For", "X-Real-IP"), // Remove proxy headers
		headers.WithSetRequest(headers.HeaderMap{
			"X-Request-Source": "go-supervisor-example", // Set request source
		}),
		headers.WithAddRequest(headers.HeaderMap{
			"X-Internal-Request": "true", // Mark as internal
		}),
		headers.WithAddRequestHeader("X-Processing-Time", time.Now().Format(time.RFC3339)),

		// Response header operations (applied after handler)
		headers.WithRemove("Server", "X-Powered-By"), // Remove server identification
		headers.WithSet(headers.HeaderMap{
			"X-Frame-Options": "DENY",             // Security header
			"X-API-Version":   "v1.0",             // API version
			"Content-Type":    "application/json", // JSON responses
		}),
		headers.WithAdd(headers.HeaderMap{
			"X-Custom-Header": "go-supervisor-headers", // Custom header
		}),
		headers.WithAddHeader("X-Response-Time", time.Now().Format(time.RFC3339)),
	)

	// Common middleware stack
	commonMw := []httpserver.HandlerFunc{
		recoveryMw,
		loggingMw,
		metricsMw,
		headersMw, // Headers middleware processes both request and response
	}

	// Header inspection handler - shows request headers received after middleware processing
	headerInspectionHandler := func(w http.ResponseWriter, r *http.Request) {
		// Collect request headers
		var requestHeaders []HeaderInfo
		for name, values := range r.Header {
			requestHeaders = append(requestHeaders, HeaderInfo{
				Name:   name,
				Values: values,
			})
		}

		// Sort headers for consistent output
		sort.Slice(requestHeaders, func(i, j int) bool {
			return requestHeaders[i].Name < requestHeaders[j].Name
		})

		response := HeaderResponse{
			Message:        "Header inspection complete - request headers show middleware effects",
			RequestHeaders: requestHeaders,
			Timestamp:      time.Now().Format(time.RFC3339),
		}

		// Response headers will be added by middleware after this handler returns
		jsonData, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		_, err = fmt.Fprint(w, string(jsonData))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
			return
		}
	}

	// Simple response handler
	simpleHandler := func(w http.ResponseWriter, r *http.Request) {
		response := map[string]interface{}{
			"message":   "Simple response with header manipulation",
			"timestamp": time.Now().Format(time.RFC3339),
			"note":      "Check response headers - they've been modified by middleware",
		}
		jsonData, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		_, err = fmt.Fprint(w, string(jsonData))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
			return
		}
	}

	// Create routes
	headerRoute, err := httpserver.NewRouteFromHandlerFunc(
		"header-inspection",
		"/headers",
		headerInspectionHandler,
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create header inspection route: %w", err)
	}

	simpleRoute, err := httpserver.NewRouteFromHandlerFunc(
		"simple",
		"/",
		simpleHandler,
		commonMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create simple route: %w", err)
	}

	// Route with different header middleware for comparison
	differentHeadersMw := headers.NewWithOperations(
		// Different request operations
		headers.WithSetRequestHeader("X-Route-Specific", "different-route"),
		headers.WithRemoveRequest("User-Agent"),

		// Different response operations
		headers.WithSetHeader("X-Route-Type", "special"),
		headers.WithAddHeader("X-Different-Header", "route-specific-value"),
	)

	specialMw := []httpserver.HandlerFunc{
		recoveryMw,
		loggingMw,
		metricsMw,
		differentHeadersMw, // Different headers middleware
	}

	specialHandler := func(w http.ResponseWriter, r *http.Request) {
		// Show headers for this route
		var requestHeaders []HeaderInfo
		for name, values := range r.Header {
			requestHeaders = append(requestHeaders, HeaderInfo{
				Name:   name,
				Values: values,
			})
		}

		sort.Slice(requestHeaders, func(i, j int) bool {
			return requestHeaders[i].Name < requestHeaders[j].Name
		})

		response := HeaderResponse{
			Message:        "Special route with different header middleware",
			RequestHeaders: requestHeaders,
			Timestamp:      time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		jsonData, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		_, err = fmt.Fprint(w, string(jsonData))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
			return
		}
	}

	specialRoute, err := httpserver.NewRouteFromHandlerFunc(
		"special",
		"/special",
		specialHandler,
		specialMw...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create special route: %w", err)
	}

	return httpserver.Routes{*simpleRoute, *headerRoute, *specialRoute}, nil
}

// createHTTPServer creates the HTTP server with configured routes
func createHTTPServer(
	routes []httpserver.Route,
	logHandler slog.Handler,
) (*httpserver.Runner, error) {
	config, err := httpserver.NewConfig(ListenOn, routes, httpserver.WithDrainTimeout(DrainTimeout))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP server config: %w", err)
	}

	return httpserver.NewRunner(
		httpserver.WithConfig(config),
		httpserver.WithLogHandler(logHandler),
	)
}

// createSupervisor initializes go-supervisor with the HTTP server
func createSupervisor(
	ctx context.Context,
	logHandler slog.Handler,
	runnable supervisor.Runnable,
) (*supervisor.PIDZero, error) {
	sv, err := supervisor.New(
		supervisor.WithContext(ctx),
		supervisor.WithLogHandler(logHandler),
		supervisor.WithRunnables(runnable))
	if err != nil {
		return nil, fmt.Errorf("failed to create supervisor: %w", err)
	}

	return sv, nil
}

func main() {
	// Configure the logger
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	// Create base context
	ctx := context.Background()

	// Build routes
	routes, err := buildRoutes(handler)
	if err != nil {
		slog.Error("Failed to build routes", "error", err)
		os.Exit(1)
	}

	// Create the HTTP server
	httpServer, err := createHTTPServer(routes, handler)
	if err != nil {
		slog.Error("Failed to create HTTP server", "error", err)
		os.Exit(1)
	}

	// Create the supervisor
	sv, err := createSupervisor(ctx, handler, httpServer)
	if err != nil {
		slog.Error("Failed to setup server", "error", err)
		os.Exit(1)
	}

	// Start the supervisor
	slog.Info("Starting headers middleware example server", "listen", ListenOn)
	slog.Info("Available endpoints:")
	slog.Info("  GET / - Simple response with header manipulation")
	slog.Info("  GET /headers - Inspect request headers (shows middleware effects)")
	slog.Info("  GET /special - Route with different header middleware")
	slog.Info("")
	slog.Info("Test with curl:")
	slog.Info(
		`  curl -H "X-Forwarded-For: 192.168.1.1" -H "User-Agent: test" -v http://localhost:8082/headers`,
	)
	slog.Info("  Notice: X-Forwarded-For is removed, custom headers are added")

	if err := sv.Run(); err != nil {
		slog.Error("Supervisor failed", "error", err)
		os.Exit(1)
	}
}
