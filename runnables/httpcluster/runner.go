package httpcluster

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/robbyt/go-supervisor/internal/finitestate"
	"github.com/robbyt/go-supervisor/runnables/httpserver"
	"github.com/robbyt/go-supervisor/supervisor"
)

const (
	defaultDeadlineServerStart = 10 * time.Second
	defaultRestartDelay        = 10 * time.Millisecond
)

// Runner manages multiple HTTP server instances as a cluster.
// It implements supervisor.Runnable and supervisor.Stateable interfaces.
type Runner struct {
	mu sync.RWMutex

	// runner factory creates the Runnable instances
	runnerFactory       runnerFactory
	restartDelay        time.Duration
	deadlineServerStart time.Duration

	// Context management - similar to composite pattern
	parentCtx context.Context // Set during construction
	runCtx    context.Context // Set during Run()
	runCancel context.CancelFunc

	// Configuration siphon channel
	configSiphon chan map[string]*httpserver.Config

	// Current entries state
	currentEntries entriesManager

	// State management using FSM
	fsm finitestate.Machine

	// Options
	logger              *slog.Logger
	stateChanBufferSize int
}

// Interface guards
var (
	_ supervisor.Runnable  = (*Runner)(nil)
	_ supervisor.Stateable = (*Runner)(nil)
)

type runnerFactory func(ctx context.Context, id string, cfg *httpserver.Config, handler slog.Handler) (httpServerRunner, error)

// defaultRunnerFactory creates a new httpserver Runnable.
func defaultRunnerFactory(
	ctx context.Context,
	id string,
	cfg *httpserver.Config,
	handler slog.Handler,
) (httpServerRunner, error) {
	return httpserver.NewRunner(
		httpserver.WithName(id),
		httpserver.WithConfig(cfg),
		httpserver.WithContext(ctx),
		httpserver.WithLogHandler(handler),
	)
}

// NewRunner creates a new HTTP cluster runner with the provided options.
func NewRunner(opts ...Option) (*Runner, error) {
	r := &Runner{
		runnerFactory:       defaultRunnerFactory,
		logger:              slog.Default().WithGroup("httpcluster.Runner"),
		restartDelay:        defaultRestartDelay,
		deadlineServerStart: defaultDeadlineServerStart,
		parentCtx:           context.Background(),
		configSiphon: make(
			chan map[string]*httpserver.Config,
		), // unbuffered by default
		currentEntries: &entries{
			servers: make(map[string]*serverEntry),
		}, // Empty initial state
		stateChanBufferSize: 10, // Default buffer size for state channels
	}

	// Apply options
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, fmt.Errorf("failed to apply option: %w", err)
		}
	}

	// Create FSM with the configured logger
	fsmLogger := r.logger.WithGroup("fsm")
	machine, err := finitestate.New(fsmLogger.Handler())
	if err != nil {
		return nil, fmt.Errorf("unable to create fsm: %w", err)
	}
	r.fsm = machine

	return r, nil
}

// GetConfigSiphon returns the configuration siphon channel for sending config updates.
func (r *Runner) GetConfigSiphon() chan<- map[string]*httpserver.Config {
	return r.configSiphon
}

// GetServerCount returns the current number of servers being managed.
func (r *Runner) GetServerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.currentEntries.count()
}

// waitForIsRunning waits for an httpserver to reach Running state.
// It returns true if the server was ready within the timeout deadline.
// If the server is in error state, it returns false.
func (r *Runner) waitForIsRunning(
	ctx context.Context,
	timeout time.Duration,
	server httpServerRunner,
) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	initialRetry := 5 * time.Millisecond
	maxRetry := 1 * time.Second

	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(initialRetry):
			if server.IsRunning() {
				return true
			}

			s := server.GetState()
			if s == "Error" || s == "Stopped" {
				return false
			}

			// Exponentially increase the backoff delay
			initialRetry = min(time.Duration(float64(initialRetry)*1.5), maxRetry)
		}
	}
}

// String returns a string representation of the cluster.
func (r *Runner) String() string {
	return fmt.Sprintf(
		"HTTPCluster[servers=%d, state=%s]",
		r.GetServerCount(),
		r.fsm.GetState())
}

// Run starts the HTTP cluster and manages all child servers.
func (r *Runner) Run(ctx context.Context) error {
	logger := r.logger.WithGroup("Run")
	logger.Debug("Starting...")

	// Transition to booting state
	if err := r.fsm.Transition(finitestate.StatusBooting); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to booting state: %w", err)
	}

	// Set up local run context, share it to make it accessible to shutdown
	r.mu.Lock()
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	r.runCtx = runCtx
	r.runCancel = runCancel
	r.mu.Unlock()

	// Transition to running (no servers initially)
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to running state: %w", err)
	}

	// Main event loop
	for {
		select {
		case <-runCtx.Done():
			logger.Debug("Run context cancelled, initiating shutdown")
			return r.shutdown(runCtx)

		case <-r.parentCtx.Done():
			logger.Debug("Parent context cancelled, initiating shutdown")
			return r.shutdown(runCtx)

		case newConfigs, ok := <-r.configSiphon:
			if !ok {
				logger.Debug("Config siphon closed, initiating shutdown")
				return r.shutdown(runCtx)
			}

			logger.Debug("Received configuration update", "serverCount", len(newConfigs))
			if err := r.processConfigUpdate(runCtx, newConfigs); err != nil {
				logger.Error("Failed to process config update", "error", err)
				// Continue running, don't fail the whole cluster
			}
		}
	}
}

// Stop signals the cluster to stop all servers and shut down.
func (r *Runner) Stop() {
	logger := r.logger.WithGroup("Stop")
	logger.Debug("Stopping")

	r.mu.Lock()
	r.runCancel()
	r.mu.Unlock()
}

// shutdown performs graceful shutdown of all servers.
func (r *Runner) shutdown(ctx context.Context) error {
	logger := r.logger.WithGroup("shutdown")
	logger.Debug("Shutting down...")
	r.mu.RLock()
	defer r.mu.RUnlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := r.fsm.Transition(finitestate.StatusStopping); err != nil {
		logger.Error("Failed to transition to stopping state", "error", err)
		// continue anyway
	}

	// Create new entries marking all servers for stop
	stopConfigs := make(map[string]*httpserver.Config)
	// Empty map means all servers should be removed
	desiredEntries := newEntries(stopConfigs)
	pendingEntries := r.currentEntries.buildPendingEntries(desiredEntries)
	updatedEntries := r.executeActions(ctx, pendingEntries)
	r.currentEntries = updatedEntries.commit()

	if err := r.fsm.Transition(finitestate.StatusStopped); err != nil {
		logger.Error("Failed to transition to stopped state", "error", err)
	}

	return nil
}

// processConfigUpdate handles configuration updates using a 2-phase commit.
func (r *Runner) processConfigUpdate(
	ctx context.Context,
	newConfigs map[string]*httpserver.Config,
) error {
	logger := r.logger.WithGroup("processConfigUpdate")
	logger.Debug("Processing config update", "count", len(newConfigs))

	// Check if we're in a state where we can process updates
	if !r.IsRunning() {
		logger.Warn("Ignoring config update - cluster not running", "state", r.fsm.GetState())
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Transition to reloading state
	if err := r.fsm.Transition(finitestate.StatusReloading); err != nil {
		return fmt.Errorf("failed to transition to reloading state: %w", err)
	}

	// Phase 1: Create new entries with pending actions
	desiredEntries := newEntries(newConfigs)
	pendingEntries := r.currentEntries.buildPendingEntries(desiredEntries)

	// Log pending actions
	toStart, toStop := pendingEntries.getPendingActions()
	logger.Debug("Pending actions calculated",
		"toStart", len(toStart),
		"toStop", len(toStop))

	// Phase 2: Execute all pending actions
	updatedEntries := r.executeActions(ctx, pendingEntries)

	// Commit: Finalize the entries state
	r.currentEntries = updatedEntries.commit()

	// Transition back to running state using conditional transition
	if err := r.fsm.TransitionIfCurrentState(finitestate.StatusReloading, finitestate.StatusRunning); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition back to running state: %w", err)
	}

	return nil
}

// executeActions orchestrates server lifecycle changes by stopping and starting servers.
func (r *Runner) executeActions(ctx context.Context, pending entriesManager) entriesManager {
	logger := r.logger.WithGroup("executeActions")
	current := pending
	toStart, toStop := pending.getPendingActions()

	// Phase 1: Stop servers that need to be stopped
	if len(toStop) > 0 {
		current = r.stopServers(ctx, current, toStop)

		// Delay after stopping servers to allow OS to release sockets
		// before binding to the same ports again
		if len(toStart) > 0 && r.restartDelay > 0 {
			logger.Debug("Waiting for ports to be released", "delay", r.restartDelay)
			select {
			case <-ctx.Done():
				logger.Debug("Context cancelled during port release wait")
				return current
			case <-time.After(r.restartDelay):
				// Continue after delay
			}
		}
	}

	// Phase 2: Start servers that need to be started
	if len(toStart) > 0 {
		current = r.startServers(ctx, current, toStart)
	}

	return current
}

// stopServers handles stopping servers and clearing their runtime state.
func (r *Runner) stopServers(
	_ context.Context,
	current entriesManager,
	toStop []string,
) entriesManager {
	logger := r.logger.WithGroup("stopServers")
	logger.Debug("Processing server stops", "count", len(toStop))

	var wg sync.WaitGroup

	// Stop all servers in parallel
	for _, id := range toStop {
		entry := current.get(id)
		logger := logger.With("id", id)
		if entry == nil || entry.runner == nil {
			logger.Debug("Skipping stop for entry - does not exist or not running")
			continue
		}

		logger.Debug("Stopping server", "addr", entry.config.ListenAddr)
		wg.Add(1)
		go func(id string, entry *serverEntry) {
			defer wg.Done()
			entry.runner.Stop()
			if entry.cancel != nil {
				entry.cancel()
			}
			logger.Debug("Server stopped", "addr", entry.config.ListenAddr)
		}(id, entry)
	}
	wg.Wait()

	// Clear runtime for stopped servers
	for _, id := range toStop {
		logger := logger.With("id", id)
		logger.Debug("Clearing runtime for stopped server")
		if updated := current.clearRuntime(id); updated != nil {
			current = updated
		}
		logger.Debug("Runtime cleared for stopped server")
	}

	return current
}

// startServers handles starting new servers and waiting for them to be ready.
func (r *Runner) startServers(
	ctx context.Context,
	current entriesManager,
	toStart []string,
) entriesManager {
	logger := r.logger.WithGroup("startServers")
	logger.Debug("Processing server starts", "count", len(toStart))

	for _, id := range toStart {
		entry := current.get(id)
		logger := logger.With("id", id)
		if entry == nil {
			logger.Debug("Skipping start - no entry found")
			continue
		}

		logger = logger.With("addr", entry.config.ListenAddr)
		logger.Debug("Starting server")

		// Create and start the server
		runner, serverCtx, serverCancel, err := r.createAndStartServer(ctx, entry)
		if err != nil {
			logger.Error("Failed to create server", "error", err)
			// Remove entry since server failed to create
			current = current.removeEntry(id)
			continue
		}

		// Update entries with runtime info
		if updated := current.setRuntime(id, runner, serverCtx, serverCancel); updated != nil {
			current = updated
		}

		// Wait for server to be ready
		if !r.waitForIsRunning(ctx, r.deadlineServerStart, runner) {
			logger.Error("Server failed to become ready",
				"timeout", r.deadlineServerStart,
				"state", runner.GetState())
			// Cancel the server context to clean up
			serverCancel()
			runner.Stop()
			// Remove entry since server failed to start
			current = current.removeEntry(id)
			continue
		}

		logger.Debug("Server instance started")
	}

	return current
}

// createAndStartServer creates a new HTTP server runner and starts it in a goroutine.
func (r *Runner) createAndStartServer(
	ctx context.Context,
	entry *serverEntry,
) (httpServerRunner, context.Context, context.CancelFunc, error) {
	// Create server context
	serverCtx, serverCancel := context.WithCancel(ctx)

	// Create the Runnable implementation, based on the factory associated with this runner
	runner, err := r.runnerFactory(serverCtx, entry.id, entry.config, r.logger.Handler())
	if err != nil {
		serverCancel()
		return nil, nil, nil, err
	}

	// Start that Runnable implementation in a goroutine
	go func(id string, runner httpServerRunner, c context.Context) {
		logger := r.logger.WithGroup("cluster").With("id", id)
		logger.Debug("Starting server instance")
		if err := runner.Run(c); err != nil {
			if c.Err() == nil {
				// Context not cancelled, this is an actual error
				logger.Error("Server instance failed", "error", err)
			}
		}
		logger.Debug("Instance stopped")
	}(entry.id, runner, serverCtx)

	return runner, serverCtx, serverCancel, nil
}
