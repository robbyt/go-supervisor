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
	"github.com/robbyt/go-supervisor/supervisor/lifecycle"
)

const (
	defaultDeadlineServerStart = 10 * time.Second
	defaultRestartDelay        = 10 * time.Millisecond
	defaultShutdownTimeout     = 5 * time.Second
)

type fsm interface {
	GetState() string
	GetStateChan(ctx context.Context) <-chan string
	Transition(state string) error
	TransitionIfCurrentState(state string, targetState string) error
	SetState(state string) error
	TransitionBool(state string) bool
}

// Runner manages multiple HTTP server instances as a cluster.
// It implements supervisor.Runnable and supervisor.Stateable interfaces.
type Runner struct {
	fsm fsm
	lc  *lifecycle.StartStop
	mu  sync.RWMutex

	// runner factory creates the Runnable instances
	runnerFactory       runnerFactory
	restartDelay        time.Duration
	deadlineServerStart time.Duration
	shutdownTimeout     time.Duration

	// Configuration siphon channel
	configSiphon chan map[string]*httpserver.Config

	// Current entries state
	currentEntries entriesManager

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
		httpserver.WithLogHandler(handler),
	)
}

// NewRunner creates a new HTTP cluster runner with the provided options.
func NewRunner(opts ...Option) (*Runner, error) {
	r := &Runner{
		lc:                  lifecycle.New(),
		runnerFactory:       defaultRunnerFactory,
		logger:              slog.Default().WithGroup("httpcluster.Runner"),
		restartDelay:        defaultRestartDelay,
		deadlineServerStart: defaultDeadlineServerStart,
		shutdownTimeout:     defaultShutdownTimeout,
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
	machine, err := finitestate.NewTypicalFSM(fsmLogger.Handler())
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

// waitForReady waits for an httpserver to finish its startup phase
// (IsReady returns true). Returns true if the server was ready within the
// timeout deadline. If the server reports Error or Stopped state, returns
// false immediately.
func (r *Runner) waitForReady(
	ctx context.Context,
	timeout time.Duration,
	server httpServerRunner,
) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	retryDelay := 5 * time.Millisecond
	maxRetry := 1 * time.Second

	timer := time.NewTimer(retryDelay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			if server.IsReady() {
				return true
			}

			s := server.GetState()
			if s == "Error" || s == "Stopped" {
				return false
			}

			retryDelay = min(time.Duration(float64(retryDelay)*1.5), maxRetry)
			timer.Reset(retryDelay)
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

	done := r.lc.Started()
	defer done()

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()

	// Transition to booting state
	if err := r.fsm.Transition(finitestate.StatusBooting); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to booting state: %w", err)
	}

	// Transition to running (no servers initially)
	if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition to running state: %w", err)
	}

	// Main event loop. Runs until any shutdown trigger fires; Run then
	// drives the bounded shutdown phase.
	r.eventLoop(runCtx)
	return r.shutdownPhase(ctx)
}

// eventLoop is the runner's config-update select loop. It returns as soon
// as any shutdown trigger fires — runCtx cancellation, Stop() (via
// r.lc.StopCh), or the configSiphon being closed — so Run can drive the
// shutdown phase from a single site. runCtx is left alive on return; Run's
// own defer runCancel() at the top of Run handles its cleanup, and the
// shutdown phase uses its own bounded context regardless.
func (r *Runner) eventLoop(runCtx context.Context) {
	logger := r.logger.WithGroup("eventLoop")
	for {
		select {
		case <-runCtx.Done():
			logger.Debug("Run context cancelled, initiating shutdown")
			return

		case <-r.lc.StopCh():
			logger.Debug("Stop() called, initiating shutdown")
			return

		case newConfigs, ok := <-r.configSiphon:
			if !ok {
				logger.Debug("Config siphon closed, initiating shutdown")
				return
			}

			logger.Debug("Received configuration update", "serverCount", len(newConfigs))
			if err := r.processConfigUpdate(runCtx, newConfigs); err != nil {
				logger.Error("Failed to process config update", "error", err)
				// Continue running, don't fail the whole cluster
			}
		}
	}
}

// shutdownPhase orchestrates the runner's shutdown sequence: it builds the
// bounded shutdown context once, spawns the siphon drain, runs the
// synchronous server shutdown, and waits for the drain to exit before
// returning. The drain is safe to run unconditionally: in the
// siphon-closed branch its receive returns ok=false immediately and the
// drain exits in one iteration.
func (r *Runner) shutdownPhase(parent context.Context) error {
	shutdownCtx, cancel := r.newShutdownContext(parent)
	defer cancel()
	drainDone := r.drainConfigSiphon(shutdownCtx)
	err := r.shutdown(shutdownCtx)
	<-drainDone
	return err
}

// newShutdownContext returns the context that bounds the runner's shutdown
// phase. Both the synchronous shutdown path and the background siphon drain
// observe this context, so a configured shutdownTimeout cascades to every
// shutdown sub-task — when the deadline fires the drain's ctx.Done case
// fires and any ctx-aware code inside shutdown unblocks.
//
// The parent's cancellation is intentionally detached via
// context.WithoutCancel: in two of the three shutdown branches the parent
// is already done (runCtx.Done fired, or Stop() just called runCancel), so
// deriving the cancellation chain would yield an already-cancelled context
// and the drain/shutdown would have no time to run. Values still propagate
// via WithoutCancel so loggers and trace IDs attached to Run's input ctx
// reach shutdown sub-tasks.
//
// The caller owns the returned cancel and is expected to defer it; the
// drain just observes the context and exits when its Done fires. A
// shutdownTimeout of zero disables the deadline; the ctx then cancels only
// when its CancelFunc is invoked.
func (r *Runner) newShutdownContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(parent)
	if r.shutdownTimeout > 0 {
		return context.WithTimeout(base, r.shutdownTimeout)
	}
	return context.WithCancel(base)
}

// drainConfigSiphon spawns a goroutine that consumes and discards values from
// r.configSiphon, unparking any external goroutines parked in a send on the
// publicly-exposed siphon channel while the main event loop tears down. It
// returns a done channel that closes when the drain has exited; Run blocks
// on this channel before returning so the drain runs for its full
// shutdownTimeout budget rather than dying the moment Run returns. The drain
// exits on (1) the inactivity window elapsing, (2) the siphon being closed,
// or (3) ctx being Done — whichever comes first. Callers should still stop
// publishing on the siphon after the supervisor reports shutdown; the drain
// only covers the race window where a send was already in flight when
// shutdown began.
func (r *Runner) drainConfigSiphon(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	const quiescence = 100 * time.Millisecond
	go func() {
		defer close(done)
		inactivity := time.NewTimer(quiescence)
		defer inactivity.Stop()
		for {
			select {
			case _, ok := <-r.configSiphon:
				if !ok {
					// Siphon closed by its owner (WithCustomSiphonChannel
					// callers can do this). Receives would otherwise return
					// immediately forever, resetting the inactivity timer on
					// every iteration and spinning until ctx.Done.
					return
				}
				if !inactivity.Stop() {
					select {
					case <-inactivity.C:
					default:
					}
				}
				inactivity.Reset(quiescence)
			case <-inactivity.C:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return done
}

// Stop signals the cluster to stop all servers and shut down.
// It blocks until Run() has completed shutdown.
func (r *Runner) Stop() {
	logger := r.logger.WithGroup("Stop")
	logger.Debug("Stopping")
	r.lc.Stop()
}

// shutdown performs graceful shutdown of all servers.
func (r *Runner) shutdown(ctx context.Context) error {
	logger := r.logger.WithGroup("shutdown")
	logger.Debug("Shutting down...")
	// Write lock: shutdown reassigns r.currentEntries (the commit below), so
	// it must exclude the RLock readers GetServerCount()/String(). Holding
	// only RLock here was a data race. The call chain (executeActions →
	// stopServers/startServers) never re-acquires r.mu, so this can't
	// self-deadlock; a concurrent removeEntryIfMatches (Lock) simply waits.
	r.mu.Lock()
	defer r.mu.Unlock()

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
	// Shutdown ignores the start-failure flag: nothing to start during shutdown,
	// and the cluster is already moving to Stopped.
	updatedEntries, _ := r.executeActions(ctx, pendingEntries)
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

	r.mu.Lock()
	defer r.mu.Unlock()

	// Admit the update and move to Reloading while holding the update lock.
	// Drive the decision off the transition *result* rather than a pre-read
	// state: a crashed backend calls setStateError() without taking r.mu, so it
	// can flip Running->Error in the window between a GetState() check and the
	// transition. Attempting the guarded transition first, and only inspecting
	// the state when it fails, closes that TOCTOU — otherwise a recovery config
	// arriving exactly as a backend crashes could still be silently dropped.
	//
	// Two entry states are admissible:
	//
	//   - Running: the normal path. The guarded TransitionIfCurrentState also
	//     covers the race where Stop() moved the cluster to Stopped before this
	//     update could enter Reloading.
	//
	//   - Error: recovery. A crashed backend (or a failed prior update) leaves
	//     the cluster in Error, and the FSM has no Error->Running edge — so
	//     without this path every subsequent update would be silently dropped
	//     and the operator could never push a corrected config. Force the FSM
	//     to Reloading via SetState (Error has no valid Transition to
	//     Reloading) and let this update rebuild the cluster.
	if err := r.fsm.TransitionIfCurrentState(
		finitestate.StatusRunning,
		finitestate.StatusReloading,
	); err != nil {
		switch state := r.fsm.GetState(); state {
		case finitestate.StatusError:
			logger.Info("Recovering cluster from Error state via config update")
			if err := r.fsm.SetState(finitestate.StatusReloading); err != nil {
				r.setStateError()
				return fmt.Errorf("failed to enter reloading state for recovery: %w", err)
			}
		default:
			logger.Warn("Ignoring config update - cluster not admissible", "state", state)
			return nil
		}
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
	updatedEntries, hadFailure := r.executeActions(ctx, pendingEntries)

	// Commit: Finalize the entries state
	r.currentEntries = updatedEntries.commit()

	// If any server failed to start, mark the cluster degraded so consumers
	// watching GetStateChan see the failure. Don't fall through to a back-to-
	// Running transition; that's the historical silent-success path.
	if hadFailure {
		r.setStateError()
		return fmt.Errorf("one or more servers failed to start during config update")
	}

	// Transition back to running state using conditional transition
	if err := r.fsm.TransitionIfCurrentState(finitestate.StatusReloading, finitestate.StatusRunning); err != nil {
		r.setStateError()
		return fmt.Errorf("failed to transition back to running state: %w", err)
	}

	return nil
}

// executeActions orchestrates server lifecycle changes by stopping and starting servers.
// The bool return is true if any server failed to start; callers use it to decide
// whether to surface the failure (e.g., transition the cluster FSM to Error).
func (r *Runner) executeActions(ctx context.Context, pending entriesManager) (entriesManager, bool) {
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
				return current, false
			case <-time.After(r.restartDelay):
				// Continue after delay
			}
		}
	}

	// Phase 2: Start servers that need to be started
	var startFailed bool
	if len(toStart) > 0 {
		current, startFailed = r.startServers(ctx, current, toStart)
	}

	return current, startFailed
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
// The bool return is true if any server failed to be created or to reach Running
// within the deadline. Failed entries are removed from the returned collection.
func (r *Runner) startServers(
	ctx context.Context,
	current entriesManager,
	toStart []string,
) (entriesManager, bool) {
	logger := r.logger.WithGroup("startServers")
	logger.Debug("Processing server starts", "count", len(toStart))

	var hadFailure bool
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
		runner, serverCancel, err := r.createAndStartServer(ctx, entry)
		if err != nil {
			logger.Error("Failed to create server", "error", err)
			hadFailure = true
			// Remove entry since server failed to create
			current = current.removeEntry(id)
			continue
		}

		// Update entries with runtime info
		if updated := current.setRuntime(id, runner, serverCancel); updated != nil {
			current = updated
		}

		// Wait for server to be ready
		if !r.waitForReady(ctx, r.deadlineServerStart, runner) {
			// Distinguish parent-ctx cancellation (the cluster is being told
			// to stop — not a server failure) from a real readiness failure
			// (timeout elapsed or server reached Error/Stopped). Only the
			// latter should trip the cluster into StatusError.
			if ctx.Err() != nil {
				logger.Debug("Server start aborted by context cancellation")
				serverCancel()
				runner.Stop()
				current = current.removeEntry(id)
				continue
			}
			logger.Error("Server failed to become ready",
				"timeout", r.deadlineServerStart,
				"state", runner.GetState())
			// Cancel the server context to clean up
			serverCancel()
			runner.Stop()
			hadFailure = true
			// Remove entry since server failed to start
			current = current.removeEntry(id)
			continue
		}

		logger.Debug("Server instance started")
	}

	return current, hadFailure
}

// createAndStartServer creates a new HTTP server runner and starts it in a goroutine.
func (r *Runner) createAndStartServer(
	ctx context.Context,
	entry *serverEntry,
) (httpServerRunner, context.CancelFunc, error) {
	// Create server context
	serverCtx, serverCancel := context.WithCancel(ctx)

	// Create the Runnable implementation, based on the factory associated with this runner
	runner, err := r.runnerFactory(serverCtx, entry.id, entry.config, r.logger.Handler())
	if err != nil {
		serverCancel()
		return nil, nil, err
	}

	// Start that Runnable implementation in a goroutine
	go func(id string, runner httpServerRunner, c context.Context, cancel context.CancelFunc) {
		// Always release this server's ctx when the goroutine exits — the
		// crash path didn't otherwise call cancel(), so per-server ctx
		// resources would have stayed referenced until the cluster runCtx
		// was cancelled (process-lifetime). Idempotent if a stop path
		// already cancelled.
		defer cancel()
		logger := r.logger.WithGroup("cluster").With("id", id)
		logger.Debug("Starting server instance")
		err := runner.Run(c)
		if err != nil && c.Err() == nil {
			// Context not cancelled, this is an actual error: mark the
			// cluster degraded so consumers watching GetStateChan see it,
			// and drop the dead entry from currentEntries.
			logger.Error("Server instance failed", "error", err)
			r.setStateError()
			r.removeEntryIfMatches(id, runner)
		}
		logger.Debug("Instance stopped")
	}(entry.id, runner, serverCtx, serverCancel)

	return runner, serverCancel, nil
}

// removeEntryIfMatches drops the entry stored under id only if it still points
// at the given runner. Called from the per-server crash handler to avoid
// racing a concurrent restart that has already committed a different runner
// under the same id — without this guard, an old goroutine's late error path
// would delete the new healthy server from currentEntries.
func (r *Runner) removeEntryIfMatches(id string, runner httpServerRunner) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur := r.currentEntries.get(id); cur != nil && cur.runner == runner {
		r.currentEntries = r.currentEntries.removeEntry(id)
	}
}
