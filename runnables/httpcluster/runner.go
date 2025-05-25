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

// Runner manages multiple HTTP server instances as a cluster.
// It implements supervisor.Runnable and supervisor.Stateable interfaces.
type Runner struct {
	mu sync.RWMutex

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
	logger *slog.Logger
}

// Interface guards
var (
	_ supervisor.Runnable  = (*Runner)(nil)
	_ supervisor.Stateable = (*Runner)(nil)
)

// NewRunner creates a new HTTP cluster runner with the provided options.
func NewRunner(opts ...Option) (*Runner, error) {
	r := &Runner{
		logger:         slog.Default().WithGroup("httpcluster"),
		parentCtx:      context.Background(),
		configSiphon:   make(chan map[string]*httpserver.Config),         // unbuffered by default
		currentEntries: &entries{servers: make(map[string]*serverEntry)}, // Empty initial state
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

// waitForServerReady waits for an httpserver to reach Running state.
// It returns true if the server was ready within the timeout deadline.
// If the server is in error state, it returns false.
func (r *Runner) waitForServerReady(
	server httpServerRunner,
	ctx context.Context,
	timeout time.Duration,
) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			if server.IsRunning() {
				return true
			}
			// Check if server is in error state
			state := server.GetState()
			if state == "Error" || state == "Stopped" {
				return false
			}
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
	logger.Info("Starting HTTP cluster")

	// Transition to booting state
	if err := r.fsm.Transition(finitestate.StatusBooting); err != nil {
		logger.Error("Failed to transition to booting state", "error", err)
		r.setStateError()
		return fmt.Errorf("failed to transition to booting state: %w", err)
	}

	// Set up run context - combines parent context and Run's context
	r.mu.Lock()
	runCtx, runCancel := context.WithCancel(ctx)
	r.runCtx = runCtx
	r.runCancel = runCancel
	r.mu.Unlock()
	defer runCancel()

	// Transition to running (no servers initially)
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		logger.Error("Failed to transition to running state", "error", err)
		r.setStateError()
		return fmt.Errorf("failed to transition to running state: %w", err)
	}

	// Main event loop
	for {
		select {
		case <-runCtx.Done():
			logger.Info("Run context cancelled, initiating shutdown")
			return r.shutdown(runCtx)

		case <-r.parentCtx.Done():
			logger.Info("Parent context cancelled, initiating shutdown")
			return r.shutdown(runCtx)

		case newConfigs, ok := <-r.configSiphon:
			if !ok {
				logger.Info("Config siphon closed, initiating shutdown")
				return r.shutdown(runCtx)
			}

			logger.Info("Received configuration update", "serverCount", len(newConfigs))
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
	logger.Info("Stopping HTTP cluster")

	r.mu.Lock()
	r.runCancel()
	r.mu.Unlock()
}

// shutdown performs graceful shutdown of all servers.
func (r *Runner) shutdown(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := r.fsm.Transition(finitestate.StatusStopping); err != nil {
		r.logger.Error("Failed to transition to stopping state", "error", err)
	}

	// Create new entries marking all servers for stop
	r.mu.Lock()
	stopConfigs := make(map[string]*httpserver.Config)
	// Empty map means all servers should be removed
	pendingEntries := newEntries(r.currentEntries.(*entries), stopConfigs, r.logger)
	r.mu.Unlock()

	// Execute the stop actions
	r.executeActions(ctx, pendingEntries)

	// Commit the changes
	r.mu.Lock()
	r.currentEntries = pendingEntries.commit()
	r.mu.Unlock()

	if err := r.fsm.Transition(finitestate.StatusStopped); err != nil {
		r.logger.Error("Failed to transition to stopped state", "error", err)
	}

	return nil
}

// processConfigUpdate handles configuration updates using a 2-phase commit.
func (r *Runner) processConfigUpdate(
	ctx context.Context,
	newConfigs map[string]*httpserver.Config,
) error {
	logger := r.logger.WithGroup("processConfigUpdate")

	// Check if we're in a state where we can process updates
	if !r.IsRunning() {
		logger.Warn("Ignoring config update - cluster not running", "state", r.fsm.GetState())
		return nil
	}

	// Transition to reloading state
	if err := r.fsm.Transition(finitestate.StatusReloading); err != nil {
		logger.Error("Failed to transition to reloading state", "error", err)
		return fmt.Errorf("failed to transition to reloading state: %w", err)
	}

	// Phase 1: Create new entries with pending actions
	r.mu.RLock()
	pendingEntries := newEntries(r.currentEntries.(*entries), newConfigs, r.logger)
	r.mu.RUnlock()

	// Log pending actions
	toStart, toStop := pendingEntries.getPendingActions()
	logger.Info("Pending actions calculated",
		"toStart", len(toStart),
		"toStop", len(toStop))

	// Phase 2: Execute all pending actions
	updatedEntries := r.executeActions(ctx, pendingEntries)

	// Commit: Finalize the entries state
	r.mu.Lock()
	r.currentEntries = updatedEntries.commit()
	r.mu.Unlock()

	// Transition back to running state using conditional transition
	if err := r.fsm.TransitionIfCurrentState(finitestate.StatusReloading, finitestate.StatusRunning); err != nil {
		logger.Error("Failed to transition back to running state", "error", err)
		r.setStateError()
		return fmt.Errorf("failed to transition back to running state: %w", err)
	}

	return nil
}

// executeActions orchestrates server lifecycle changes by stopping and starting servers.
func (r *Runner) executeActions(ctx context.Context, pending entriesManager) entriesManager {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	current := pending
	toStart, toStop := pending.getPendingActions()

	// Phase 1: Stop servers that need to be stopped
	current = r.stopServers(ctx, current, toStop)

	// Phase 2: Start servers that need to be started
	current = r.startServers(ctx, current, toStart)

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
		if entry == nil || entry.runner == nil {
			logger.Debug("Skipping stop - no running server", "id", id)
			continue
		}

		logger.Debug("Stopping server", "id", id, "addr", entry.config.ListenAddr)
		wg.Add(1)
		go func(id string, entry *serverEntry) {
			defer wg.Done()
			entry.runner.Stop()
			if entry.cancel != nil {
				entry.cancel()
			}
		}(id, entry)
	}
	wg.Wait()

	// Clear runtime for stopped servers
	for _, id := range toStop {
		logger.Debug("Clearing runtime for stopped server", "id", id)
		if updated := current.clearRuntime(id); updated != nil {
			current = updated
		}
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
		if entry == nil {
			logger.Debug("Skipping start - no entry found", "id", id)
			continue
		}

		logger.Debug("Starting server", "id", id, "addr", entry.config.ListenAddr)

		// Create and start the server
		runner, serverCtx, serverCancel, err := r.createAndStartServer(ctx, entry)
		if err != nil {
			logger.Error("Failed to create server", "id", id, "error", err)
			continue
		}

		// Get state channel
		stateSub := runner.GetStateChan(serverCtx)

		// Update entries with runtime info
		if updated := current.setRuntime(id, runner, serverCtx, serverCancel, stateSub); updated != nil {
			current = updated
		}

		// Wait for server to be ready
		if !r.waitForServerReady(runner, serverCtx, 10*time.Second) {
			logger.Error("Server failed to become ready", "id", id)
			// Cancel the server context to clean up
			serverCancel()
			// Clear runtime info since server failed to start
			if updated := current.clearRuntime(id); updated != nil {
				current = updated
			}
			continue
		}

		logger.Debug("Server started successfully", "id", id, "state", runner.GetState())
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

	// Create httpserver runner
	runner, err := httpserver.NewRunner(
		httpserver.WithConfig(entry.config),
		httpserver.WithContext(serverCtx),
		httpserver.WithLogHandler(r.logger.Handler()),
	)
	if err != nil {
		serverCancel()
		return nil, nil, nil, err
	}

	// Start server in goroutine
	go func(id string, runner httpServerRunner, ctx context.Context) {
		logger := r.logger.WithGroup("serverGoroutine")
		logger.Debug("Starting server goroutine", "id", id)
		if err := runner.Run(ctx); err != nil {
			if ctx.Err() == nil {
				// Context not cancelled, this is an actual error
				logger.Error("Server failed", "id", id, "error", err)
			}
		}
		logger.Debug("Server goroutine stopped", "id", id)
	}(entry.id, runner, serverCtx)

	return runner, serverCtx, serverCancel, nil
}
