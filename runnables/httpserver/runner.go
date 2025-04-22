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

// Option represents a functional option for configuring Runner.
type Option func(*Runner)

// WithLogHandler sets a custom slog handler for the Runner instance.
// For example, to use a custom JSON handler with debug level:
//
//	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
//	runner, err := httpserver.NewRunner(ctx, httpserver.WithConfigCallback(configCallback), httpserver.WithLogHandler(handler))
func WithLogHandler(handler slog.Handler) Option {
	return func(r *Runner) {
		if handler != nil {
			r.logger = slog.New(handler.WithGroup("httpserver.Runner"))
		}
	}
}

// WithContext sets a custom context for the Runner instance.
// This allows for more granular control over cancellation and timeouts.
func WithContext(ctx context.Context) Option {
	return func(r *Runner) {
		if ctx != nil {
			r.ctx, r.cancel = context.WithCancel(ctx)
		}
	}
}

// WithConfigCallback sets the function that will be called to load or reload configuration.
// This option is required when creating a new Runner.
func WithConfigCallback(callback func() (*Config, error)) Option {
	return func(r *Runner) {
		r.configCallback = callback
	}
}

// WithName sets the name of the Runner instance.
func WithName(name string) Option {
	return func(r *Runner) {
		r.name = name
	}
}

// Runner implements a configurable HTTP server that supports graceful shutdown,
// dynamic reconfiguration, and state monitoring. It meets the Runnable, Reloadable,
// and Stateable interfaces from the supervisor package.
type Runner struct {
	name           string
	config         atomic.Pointer[Config]
	configCallback func() (*Config, error)
	bootLock       sync.Mutex
	server         *http.Server
	serverRunning  atomic.Bool
	serverErrors   chan error
	fsm            finitestate.Machine // implemented by go-fsm
	ctx            context.Context
	cancel         context.CancelFunc
	logger         *slog.Logger
}

// Interface guards to ensure all of these are implemented
var (
	_ supervisor.Runnable   = (*Runner)(nil)
	_ supervisor.Reloadable = (*Runner)(nil)
	_ supervisor.Stateable  = (*Runner)(nil)
)

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

	r.bootLock.Lock()
	err = r.boot()
	r.bootLock.Unlock()

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

	r.bootLock.Lock()
	err = r.stopServer(runCtx)
	r.bootLock.Unlock()

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
		return errors.New("no config set")
	}

	addr := cfg.ListenAddr
	mux := cfg.getMux()

	r.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	r.logger.Info("Starting HTTP server", "listenOn", addr)
	go func() {
		if !r.serverRunning.CompareAndSwap(false, true) {
			r.serverErrors <- errors.New("HTTP server already running")
			return
		}

		if err := r.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			r.serverErrors <- err
		}
		r.serverRunning.Store(false)
		r.logger.Debug("HTTP server stopped", "listenOn", addr)
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

	if !r.serverRunning.Load() {
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
