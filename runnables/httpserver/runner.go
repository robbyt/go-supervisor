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

// Runner implements a configurable HTTP server that supports graceful shutdown,
// dynamic reconfiguration, and state monitoring. It meets the Runnable, Reloadable,
// and Stateable interfaces from the supervisor package.
type Runner struct {
	name           string
	config         atomic.Pointer[Config]
	configCallback ConfigCallback
	mutex          sync.RWMutex

	server          HttpServer
	serverCloseOnce sync.Once
	serverMutex     sync.RWMutex
	serverErrors    chan error

	fsm    finitestate.Machine
	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
}

// NewRunner initializes a new HTTPServer runner instance.
func NewRunner(opts ...Option) (*Runner, error) {
	// Set default logger
	logger := slog.Default().WithGroup("httpserver.Runner")

	// Initialize with a background context by default
	ctx, cancel := context.WithCancel(context.Background())

	r := &Runner{
		name:            "",
		config:          atomic.Pointer[Config]{},
		serverCloseOnce: sync.Once{},
		serverErrors:    make(chan error, 1),
		ctx:             ctx,
		cancel:          cancel,
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

// Run starts the HTTP server and listens for incoming requests
func (r *Runner) Run(ctx context.Context) error {
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

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
		r.logger.Debug("Local context canceled")
	case <-r.ctx.Done():
		r.logger.Debug("Parent context canceled")
	case err := <-r.serverErrors:
		r.setStateError()
		return fmt.Errorf("%w: %w", ErrHttpServer, err)
	}

	// Try to transition to Stopping state
	if !r.fsm.TransitionBool(finitestate.StatusStopping) {
		// If already in Stopping state, this is okay and we can continue
		if r.fsm.GetState() == finitestate.StatusStopping {
			r.logger.Debug("Already in Stopping state, continuing shutdown")
		} else {
			// Otherwise, this is a real failure
			r.setStateError()
			return fmt.Errorf("%w: transition to Stopping state", ErrStateTransition)
		}
	}

	r.mutex.Lock()
	err = r.stopServer(runCtx)
	r.mutex.Unlock()

	if err != nil {
		r.setStateError()
		// Return the error directly so it can be checked with errors.Is
		return err
	}

	err = r.fsm.Transition(finitestate.StatusStopped)
	if err != nil {
		r.setStateError()
		return err
	}

	r.logger.Debug("HTTP server shut down gracefully")
	return nil
}

// Stop will cancel the parent context, which will close the HTTP server
func (r *Runner) Stop() {
	// Only transition to Stopping if we're currently Running
	err := r.fsm.TransitionIfCurrentState(finitestate.StatusRunning, finitestate.StatusStopping)
	if err != nil {
		// This error is expected if we're already stopping, so only log at debug level
		r.logger.Debug("Note: Not transitioning to Stopping state", "error", err)
	}
	r.cancel()
}

// serverReadinessProbe checks if the HTTP server is listening and accepting connections
// by attempting to establish a TCP connection to the server's address
func (r *Runner) serverReadinessProbe(ctx context.Context, addr string) error {
	// Create a dialer with timeout
	dialer := &net.Dialer{
		Timeout: 100 * time.Millisecond,
	}

	// Set up a timeout context for the entire probe operation
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Retry loop - attempt to connect until success or timeout
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-probeCtx.Done():
			return fmt.Errorf("%w: %w", ErrServerReadinessTimeout, probeCtx.Err())
		case <-ticker.C:
			// Attempt to establish a TCP connection
			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err == nil {
				// Connection successful, server is accepting connections
				if err := conn.Close(); err != nil {
					// Check if it's a "closed network connection" error
					if !errors.Is(err, net.ErrClosed) {
						r.logger.Warn("Error closing connection", "error", err)
					}
				}
				return nil
			}

			// Connection failed, log and retry
			r.logger.Debug("Server not ready yet, retrying", "error", err)
		}
	}
}

func (r *Runner) boot() error {
	originalCfg := r.getConfig()
	if originalCfg == nil {
		return ErrRetrieveConfig
	}

	// Create a new Config with the same settings but use the Runner's context
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

	// Create the server, and reset the serverCloseOnce with a mutex
	r.serverMutex.Lock()
	r.server = serverCfg.createServer()
	r.serverCloseOnce = sync.Once{}
	r.serverMutex.Unlock()

	r.logger.Info("Starting HTTP server",
		"listenOn", listenAddr,
		"readTimeout", serverCfg.ReadTimeout,
		"writeTimeout", serverCfg.WriteTimeout,
		"idleTimeout", serverCfg.IdleTimeout,
		"drainTimeout", serverCfg.DrainTimeout)

	// Start the server in a goroutine
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

	// Wait for the server to be ready or fail
	if err := r.serverReadinessProbe(r.ctx, listenAddr); err != nil {
		// If probe fails, attempt to stop the server since it may be partially started
		if err := r.stopServer(r.ctx); err != nil {
			r.logger.Warn("Error stopping server", "error", err)
		}
		return fmt.Errorf("%w: %w", ErrServerBoot, err)
	}

	// Get the actual listening address for auto-assigned ports
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

// setConfig atomically updates the current configuration
func (r *Runner) setConfig(config *Config) {
	r.config.Store(config)
	r.logger.Debug("Config updated", "config", config)
}

// getConfig returns the current configuration, loading it via the callback if necessary
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

func (r *Runner) stopServer(ctx context.Context) error {
	var shutdownErr error
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
		shutdownCtx, shutdownCancel := context.WithTimeout(ctx, drainTimeout)
		defer shutdownCancel()

		localErr := r.server.Shutdown(shutdownCtx)

		// Check if the context deadline was exceeded, regardless of the error from Shutdown
		select {
		case <-shutdownCtx.Done():
			if errors.Is(shutdownCtx.Err(), context.DeadlineExceeded) {
				r.logger.Warn("Shutdown timeout reached, some connections may have been terminated")
				shutdownErr = fmt.Errorf("%w: %w", ErrGracefulShutdownTimeout, shutdownCtx.Err())
				return
			}
		default:
			// Context not done, normal shutdown
		}

		// Handle any other error from shutdown
		if localErr != nil {
			shutdownErr = fmt.Errorf("%w: %w", ErrGracefulShutdown, localErr)
			return
		}
	})

	// if stopServer is called, always reset the server reference
	r.serverMutex.Lock()
	r.server = nil
	r.serverMutex.Unlock()

	return shutdownErr
}
