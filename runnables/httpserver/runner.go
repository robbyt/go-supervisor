package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// Interface guards verify implementation at compile time
var (
	_ supervisor.Runnable   = (*Runner)(nil)
	_ supervisor.Reloadable = (*Runner)(nil)
	_ supervisor.Stateable  = (*Runner)(nil)
)

// ConfigCallback is the function type signature for the callback used to load initial config, and new config during Reload()
type ConfigCallback func() (*Config, error)

// HttpServer is the interface for the HTTP server
type HttpServer interface {
	ListenAndServe() error
	Shutdown(ctx context.Context) error
}

type fsm interface {
	GetState() string
	GetStateChanWithTimeout(ctx context.Context) <-chan string
	Transition(state string) error
	SetState(state string) error
	TransitionBool(state string) bool
}

// Runner implements an HTTP server with graceful shutdown, dynamic reconfiguration,
// and state monitoring. It implements the Runnable, Reloadable, and Stateable
// interfaces from the supervisor package.
type Runner struct {
	fsm            fsm
	mutex          sync.RWMutex
	name           string
	config         atomic.Pointer[Config]
	configCallback ConfigCallback

	server          HttpServer
	serverCloseOnce sync.Once
	serverMutex     sync.RWMutex
	serverErrors    chan error

	// Set during Run()
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
}

// NewRunner creates a new HTTP server runner instance with the provided options.
func NewRunner(opts ...Option) (*Runner, error) {
	// Set default logger
	logger := slog.Default().WithGroup("httpserver.Runner")

	r := &Runner{
		name:            "",
		config:          atomic.Pointer[Config]{},
		serverCloseOnce: sync.Once{},
		serverErrors:    make(chan error, 1),
		logger:          logger,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	// Validate required options
	if r.configCallback == nil {
		return nil, fmt.Errorf("config callback is required (use WithConfigCallback)")
	}

	// Create FSM with the configured logger
	fsmLogger := r.logger.WithGroup("fsm")
	machine, err := finitestate.New(fsmLogger.Handler())
	if err != nil {
		return nil, fmt.Errorf("unable to create fsm: %w", err)
	}
	r.fsm = machine

	// Load initial config
	if cfg := r.getConfig(); cfg == nil {
		return nil, fmt.Errorf("%w: initial configuration", ErrConfigCallback)
	}

	return r, nil
}

// String returns a string representation of the HTTPServer instance
func (r *Runner) String() string {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	args := make([]string, 0)
	if r.name != "" {
		args = append(args, "name: "+r.name)
	}
	if cfg := r.getConfig(); cfg != nil {
		args = append(args, "listening: "+cfg.ListenAddr)
	}
	if len(args) == 0 {
		return "HTTPServer<>"
	}

	return fmt.Sprintf("HTTPServer{%s}", strings.Join(args, ", "))
}

// Run starts the HTTP server and handles its lifecycle. It transitions through
// FSM states and returns when the server is stopped or encounters an error.
func (r *Runner) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Store the context and cancel function
	r.mutex.Lock()
	r.ctx = runCtx
	r.cancel = runCancel
	r.mutex.Unlock()

	// Transition from New to Booting
	err := r.fsm.Transition(finitestate.StatusBooting)
	if err != nil {
		return err
	}

	r.mutex.Lock()
	err = r.boot()
	r.mutex.Unlock()

	if err != nil {
		r.setStateError()
		return fmt.Errorf("%w: %w", ErrServerBoot, err)
	}

	// Transition from Booting to Running
	err = r.fsm.Transition(finitestate.StatusRunning)
	if err != nil {
		r.setStateError()
		return err
	}

	select {
	case <-runCtx.Done():
		r.logger.Debug("Context canceled")
	case err := <-r.serverErrors:
		r.setStateError()
		return fmt.Errorf("%w: %w", ErrHttpServer, err)
	}

	return r.shutdown(runCtx)
}

// Stop signals the HTTP server to shut down by canceling its context.
func (r *Runner) Stop() {
	r.logger.Debug("Stopping HTTP server")

	r.mutex.RLock()
	cancel := r.cancel
	r.mutex.RUnlock()

	if cancel == nil {
		r.logger.Warn("Cancel function is nil, skipping Stop")
		return
	}
	cancel()
}

// serverReadinessProbe verifies the HTTP server is accepting connections by
// repeatedly attempting TCP connections until success or timeout.
func (r *Runner) serverReadinessProbe(ctx context.Context, addr string) error {
	// Configure TCP dialer with connection timeout
	dialer := &net.Dialer{
		Timeout: 100 * time.Millisecond,
	}

	// Set timeout for the readiness probe operation
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Retry connection attempts until success or timeout
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-r.serverErrors:
			return fmt.Errorf("server failed to start: %w", err)
		case <-probeCtx.Done():
			return fmt.Errorf("%w: %w", ErrServerReadinessTimeout, probeCtx.Err())
		case <-ticker.C:
			// Attempt to establish a TCP connection
			conn, err := dialer.DialContext(probeCtx, "tcp", addr)
			if err == nil {
				// Server is ready and accepting connections
				if err := conn.Close(); err != nil {
					// Ignore expected connection close errors
					if !errors.Is(err, net.ErrClosed) {
						r.logger.Warn("Error closing connection", "error", err)
					}
				}
				return nil
			}

			// Connection failed, continue retrying
			r.logger.Debug("Server not ready yet, retrying", "error", err)
		}
	}
}

func (r *Runner) boot() error {
	originalCfg := r.getConfig()
	if originalCfg == nil {
		return ErrRetrieveConfig
	}

	// Create server config using Runner's context for request handling
	serverCfg, err := NewConfig(
		originalCfg.ListenAddr,
		originalCfg.Routes,
		WithConfigCopy(originalCfg), // Copy all other settings
		WithRequestContext(r.ctx),   // Use the Runner's context
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCreateConfig, err)
	}

	listenAddr := serverCfg.ListenAddr

	// Initialize server instance and reset shutdown guard
	r.serverMutex.Lock()
	r.server = serverCfg.createServer()
	r.serverCloseOnce = sync.Once{}
	r.serverMutex.Unlock()

	r.logger.Debug("Starting HTTP server",
		"listenOn", listenAddr,
		"readTimeout", serverCfg.ReadTimeout,
		"writeTimeout", serverCfg.WriteTimeout,
		"idleTimeout", serverCfg.IdleTimeout,
		"drainTimeout", serverCfg.DrainTimeout)

	// Start HTTP server in background goroutine
	go func() {
		r.serverMutex.RLock()
		server := r.server
		r.serverMutex.RUnlock()

		if server == nil {
			r.logger.Debug("Server was nil, not starting")
			return
		}

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.serverErrors <- err
		}
		r.logger.Debug("HTTP server stopped", "listenOn", listenAddr)
	}()

	// Verify server is ready to accept connections
	if err := r.serverReadinessProbe(r.ctx, listenAddr); err != nil {
		// Clean up partially started server on readiness failure
		if err := r.stopServer(r.ctx); err != nil {
			r.logger.Warn("Error stopping server", "error", err)
		}
		return fmt.Errorf("%w: %w", ErrServerBoot, err)
	}

	// Retrieve actual listening address for port 0 assignments
	actualAddr := listenAddr
	r.serverMutex.RLock()
	if tcpAddr, ok := r.server.(interface{ Addr() net.Addr }); ok && tcpAddr.Addr() != nil {
		actualAddr = tcpAddr.Addr().String()
	}
	r.serverMutex.RUnlock()

	r.logger.Debug("HTTP server is ready",
		"addr", actualAddr)

	return nil
}

// setConfig atomically stores the new configuration.
func (r *Runner) setConfig(config *Config) {
	r.config.Store(config)
	r.logger.Debug("Config updated", "config", config)
}

// getConfig returns the current configuration, loading it via callback if not set.
func (r *Runner) getConfig() *Config {
	config := r.config.Load()
	if config != nil {
		return config
	}

	r.logger.Debug("Loading new config via callback")
	newConfig, err := r.configCallback()
	if err != nil {
		r.logger.Error("Failed to load config", "error", err)
		return nil
	}

	if newConfig == nil {
		r.logger.Error("Config callback returned nil")
		return nil
	}

	r.setConfig(newConfig)
	return newConfig
}

// stopServer performs graceful HTTP server shutdown with timeout handling.
// It uses sync.Once to ensure shutdown occurs only once per server instance.
func (r *Runner) stopServer(ctx context.Context) error {
	var shutdownErr error
	//nolint:contextcheck // We intentionally use context.Background() for shutdown timeout
	r.serverCloseOnce.Do(func() {
		r.serverMutex.RLock()
		defer r.serverMutex.RUnlock()
		if r.server == nil {
			shutdownErr = ErrServerNotRunning
			return
		}
		r.logger.Debug("Stopping HTTP server")

		cfg := r.getConfig()
		drainTimeout := 5 * time.Second
		if cfg != nil {
			drainTimeout = cfg.DrainTimeout
		} else {
			r.logger.Warn("Config missing, using default drain timeout")
		}

		r.logger.Debug("Waiting for graceful HTTP server shutdown...", "timeout", drainTimeout)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), drainTimeout)
		defer shutdownCancel()

		localErr := r.server.Shutdown(shutdownCtx)

		// Detect timeout regardless of Shutdown() return value
		select {
		case <-shutdownCtx.Done():
			if errors.Is(shutdownCtx.Err(), context.DeadlineExceeded) {
				r.logger.Warn("Shutdown timeout reached, some connections may have been terminated")
				shutdownErr = fmt.Errorf("%w: %w", ErrGracefulShutdownTimeout, shutdownCtx.Err())
				return
			}
		default:
			// Shutdown completed within timeout
		}

		// Handle other shutdown errors
		if localErr != nil {
			shutdownErr = fmt.Errorf("%w: %w", ErrGracefulShutdown, localErr)
			return
		}
	})

	// Reset server reference after shutdown attempt
	r.serverMutex.Lock()
	r.server = nil
	r.serverMutex.Unlock()

	return shutdownErr
}

// shutdown coordinates HTTP server shutdown with FSM state management.
// It transitions to Stopping state, calls stopServer, then transitions to Stopped.
func (r *Runner) shutdown(ctx context.Context) error {
	logger := r.logger.WithGroup("shutdown")
	logger.Debug("Shutting down HTTP server")

	// Begin shutdown by transitioning to Stopping state
	if err := r.fsm.Transition(finitestate.StatusStopping); err != nil {
		logger.Error("Failed to transition to stopping state", "error", err)
		// Continue shutdown even if state transition fails
	}

	r.mutex.Lock()
	err := r.stopServer(ctx)
	r.mutex.Unlock()

	if err != nil {
		r.setStateError()
		return err
	}

	if err := r.fsm.Transition(finitestate.StatusStopped); err != nil {
		r.setStateError()
		return err
	}

	logger.Debug("HTTP server shutdown complete")
	return nil
}
