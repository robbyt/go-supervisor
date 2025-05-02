package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/supervisor"
)

// Interface guards to ensure all of these are implemented
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
	mutex          sync.Mutex
	server         HttpServer
	serverErrors   chan error
	fsm            finitestate.Machine
	ctx            context.Context
	cancel         context.CancelFunc
	logger         *slog.Logger
}

// NewRunner initializes a new HTTPServer runner instance.
func NewRunner(opts ...Option) (*Runner, error) {
	// Set default logger
	logger := slog.Default().WithGroup("httpserver.Runner")

	// Initialize with a background context by default
	ctx, cancel := context.WithCancel(context.Background())

	r := &Runner{
		name:         "",
		config:       atomic.Pointer[Config]{},
		serverErrors: make(chan error, 1),
		ctx:          ctx,
		cancel:       cancel,
		logger:       logger,
	}

	// Apply options
	for _, opt := range opts {
		opt(r)
	}

	// Validate required options
	if r.configCallback == nil {
		return nil, errors.New("config callback is required (use WithConfigCallback)")
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
		return nil, errors.New("failed to load initial config")
	}

	return r, nil
}

// String returns a string representation of the HTTPServer instance
func (r *Runner) String() string {
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
		return fmt.Errorf("failed to start HTTP server: %w", err)
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
			return fmt.Errorf("failed to transition to Stopping state")
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

func (r *Runner) boot() error {
	cfg := r.getConfig()
	if cfg == nil {
		return errors.New("failed to retrieve config")
	}

	// Use the Config's CreateServer callback to create the HttpServer implementation
	r.server = cfg.createServer()

	r.logger.Info("Starting HTTP server", "listenOn", cfg.ListenAddr)
	go func() {
		if err := r.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.serverErrors <- err
		}
		r.logger.Debug("HTTP server stopped", "listenOn", cfg.ListenAddr)
	}()

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
	if r.server == nil {
		return errors.New("server not running")
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

	shutdownErr := r.server.Shutdown(shutdownCtx)

	// Check if the context deadline was exceeded, regardless of the error from Shutdown
	select {
	case <-shutdownCtx.Done():
		if errors.Is(shutdownCtx.Err(), context.DeadlineExceeded) {
			r.logger.Warn("Shutdown timeout reached, some connections may have been terminated")
			return fmt.Errorf("%w: %w", ErrGracefulShutdownTimeout, shutdownCtx.Err())
		}
	default:
		// Context not done, normal shutdown
	}

	// Handle any other error from shutdown
	if shutdownErr != nil {
		return fmt.Errorf("%w: %w", ErrGracefulShutdown, shutdownErr)
	}

	return nil
}
