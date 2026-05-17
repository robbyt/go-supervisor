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
	"github.com/robbyt/go-supervisor/supervisor/lifecycle"
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
	GetStateChan(ctx context.Context) <-chan string
	Transition(state string) error
	TransitionIfCurrentState(state, targetState string) error
	SetState(state string) error
	TransitionBool(state string) bool
}

// Runner implements an HTTP server with graceful shutdown, dynamic reconfiguration,
// and state monitoring. It implements the Runnable, Reloadable, and Stateable
// interfaces from the supervisor package.
type Runner struct {
	fsm            fsm
	lc             *lifecycle.StartStop
	mutex          sync.RWMutex
	name           string
	config         atomic.Pointer[Config]
	configMu       sync.Mutex
	configCallback ConfigCallback

	reloadCh chan *reloadReq

	server          HttpServer
	serverCloseOnce sync.Once
	serverMutex     sync.RWMutex
	serverErrors    chan error

	logger *slog.Logger
}

// reloadReq carries an accepted reload from Reload(ctx) into Run's event loop
// so executeReload runs with runCtx (matching the runner's lifetime) rather
// than the caller's ctx. result is the result channel: Run sends the reload
// outcome on it (nil on success, non-nil on failure) — including from
// drainReloadCh on Run exit, which sends the abandonment sentinel — so a
// caller blocked on the receive always unblocks with a meaningful value. The
// channel doubles as both the completion signal and the error carrier; no
// shared mutable field needed.
type reloadReq struct {
	cfg    *Config
	result chan error
}

// NewRunner creates a new HTTP server runner instance with the provided options.
func NewRunner(opts ...Option) (*Runner, error) {
	// Set default logger
	logger := slog.Default().WithGroup("httpserver.Runner")

	r := &Runner{
		lc:              lifecycle.New(),
		name:            "",
		config:          atomic.Pointer[Config]{},
		serverCloseOnce: sync.Once{},
		serverErrors:    make(chan error, 1),
		reloadCh:        make(chan *reloadReq, 1),
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
	machine, err := finitestate.NewTypicalFSM(fsmLogger.Handler())
	if err != nil {
		return nil, fmt.Errorf("unable to create fsm: %w", err)
	}
	r.fsm = machine

	// Load initial config
	if _, err := r.getConfig(); err != nil {
		return nil, fmt.Errorf("initial configuration: %w", err)
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
	cfg, err := r.getConfig()
	if err != nil {
		r.logger.Debug("String: config unavailable", "error", err)
	} else if cfg != nil {
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
	// Defer order matters: drainReloadCh runs LAST so it catches any reload
	// request that arrived after lc.done() closed DoneCh but before this
	// function returned. lc.done() runs before drainReloadCh so DoneCh-based
	// callers see the runner as stopped while we drain stragglers.
	defer r.drainReloadCh()
	done := r.lc.Started()
	defer done()

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Transition from New to Booting
	err := r.fsm.Transition(finitestate.StatusBooting)
	if err != nil {
		return err
	}

	r.mutex.Lock()
	err = r.boot(runCtx)
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

	if err := r.waitForEvent(runCtx); err != nil {
		return err
	}
	runCancel()

	r.drainReloadCh()

	return r.shutdown(runCtx)
}

// waitForEvent blocks until context cancellation, stop signal, or a server
// error. Reload requests are processed inline so executeReload runs with
// ctx (= Run's runCtx), giving the new server's BaseContext a lifetime tied
// to the runner rather than the caller of Reload().
func (r *Runner) waitForEvent(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("Local context canceled")
			return nil
		case <-r.lc.StopCh():
			r.logger.Debug("Stop() called")
			return nil
		case err := <-r.serverErrors:
			r.setStateError()
			return fmt.Errorf("%w: %w", ErrHttpServer, err)
		case req := <-r.reloadCh:
			req.result <- r.handleReload(ctx, req.cfg)
		}
	}
}

// handleReload runs an accepted reload request. Reload has already moved the
// FSM from Running to Reloading, so this only completes the restart and then
// returns the FSM to Running (or Error on real failure). Returns the reload
// outcome so the caller (Run's event loop) owns the channel-protocol step
// (req.result <- err).
//
// Cancellation (context.Canceled / DeadlineExceeded) is treated as control
// flow, not failure: the runner is shutting down or the caller asked to
// abort. We try to transition back to Running so Run's subsequent
// Stopping/Stopped sequence stays valid; if that transition fails the
// runner is already terminal and we accept whatever state it's in. The
// cancellation error still propagates to the caller.
func (r *Runner) handleReload(ctx context.Context, cfg *Config) error {
	if err := r.executeReload(ctx, cfg); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			r.logger.Debug("Reload aborted by ctx cancellation", "error", err)
			if transErr := r.fsm.TransitionIfCurrentState(
				finitestate.StatusReloading, finitestate.StatusRunning,
			); transErr != nil {
				r.logger.Debug("Could not transition Reloading→Running after ctx-cancel",
					"error", transErr)
			}
			return err
		}
		r.logger.Error("Reload failed", "error", err)
		r.setStateError()
		return err
	}

	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.logger.Error("Failed to transition from Reloading to Running", "error", err)
		r.setStateError()
		return err
	}
	return nil
}

// drainReloadCh sends an abandonment error on req.result for any reload
// request still buffered in reloadCh after Run's select loop exits. Without
// this, a Reload caller blocked on the receive would only unblock via
// lc.DoneCh() in its outer select — sending here makes the protocol explicit
// and unblocks the caller's result branch deterministically.
func (r *Runner) drainReloadCh() {
	for {
		select {
		case req := <-r.reloadCh:
			// Surface the abandonment via req.result so Reload's
			// <-req.result branch returns a non-nil error per T3.1.
			// Without this, an accepted-then-drained reload would
			// silently look like success.
			req.result <- errors.New("runner stopped before reload was handled")
		default:
			return
		}
	}
}

// Stop signals the HTTP server to begin graceful shutdown and blocks until
// Run() completes. http.Server.Shutdown is invoked with a timeout of
// Config.DrainTimeout, giving in-flight HTTP requests up to that long to
// complete before Shutdown returns. This drain window is decoupled from
// the context passed to Run() — cancelling that context does not shorten
// the wait. Note that the server's BaseContext is derived from Run's ctx,
// so request handlers that honor r.Context() will still observe
// cancellation independently of the drain timeout. See stopServer for the
// rationale.
func (r *Runner) Stop() {
	r.logger.Debug("Stopping HTTP server")
	r.lc.Stop()
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

func (r *Runner) boot(ctx context.Context) error {
	originalCfg, err := r.getConfig()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRetrieveConfig, err)
	}
	if originalCfg == nil {
		return ErrRetrieveConfig
	}

	serverCfg, err := NewConfig(
		originalCfg.ListenAddr,
		originalCfg.Routes,
		WithConfigCopy(originalCfg), // Copy all other settings
		WithRequestContext(ctx),
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
	if err := r.serverReadinessProbe(ctx, listenAddr); err != nil {
		if err := r.stopServer(ctx); err != nil {
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

// getConfig returns the current configuration, loading it via callback if not
// set. The hot path is an atomic load with no synchronization; the cold path
// (config currently nil) uses double-checked locking on configMu to serialize
// callback execution, so concurrent callers cannot all invoke the callback in
// parallel and orphan each other's Config. The callback may still run more
// than once across calls — if it returns an error or nil, r.config stays
// nil and the next caller retries (serialized, not concurrent). Mirrors
// composite.Runner.getConfig.
//
// On callback failure the returned error wraps ErrConfigCallback together
// with the underlying error; on a nil-from-callback the returned error is
// ErrConfigCallbackNil. Callers that only want the cached value (display
// paths, opportunistic reads) should call r.config.Load() directly to avoid
// triggering the callback at all.
func (r *Runner) getConfig() (*Config, error) {
	if config := r.config.Load(); config != nil {
		return config, nil
	}

	r.configMu.Lock()
	defer r.configMu.Unlock()

	// Recheck under the lock: another caller that won the race has
	// already populated config while we were blocked.
	if config := r.config.Load(); config != nil {
		return config, nil
	}

	r.logger.Debug("Loading new config via callback")
	newConfig, err := r.configCallback()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrConfigCallback, err)
	}
	if newConfig == nil {
		return nil, ErrConfigCallbackNil
	}

	r.setConfig(newConfig)
	return newConfig, nil
}

// stopServer invokes http.Server.Shutdown with a timeout of
// Config.DrainTimeout, giving in-flight requests a bounded window to
// complete before Shutdown returns. The shutdown context is derived from
// context.Background() rather than the parent ctx because Run() calls
// runCancel() before invoking shutdown(), so on the normal shutdown path
// the parent ctx is already done here — a ctx-derived timeout would fire
// immediately and Shutdown would return without waiting for the drain.
//
// http.Server.Shutdown does not itself terminate in-flight handlers when
// its ctx fires; it just stops waiting and returns. Active requests
// continue running on connections that outlive Shutdown.
//
// sync.Once ensures shutdown runs at most once per server instance.
func (r *Runner) stopServer(ctx context.Context) error {
	var shutdownErr error
	//nolint:contextcheck // intentional: Run cancels runCtx before shutdown(); a ctx-derived timeout would fire immediately and skip the drain
	r.serverCloseOnce.Do(func() {
		r.serverMutex.RLock()
		defer r.serverMutex.RUnlock()
		if r.server == nil {
			shutdownErr = ErrServerNotRunning
			return
		}
		r.logger.Debug("Stopping HTTP server")

		// Read the cached config directly: this is a shutdown path and
		// has no business triggering the callback. Falls back to a sane
		// default if no config has ever been loaded.
		cfg := r.config.Load()
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
