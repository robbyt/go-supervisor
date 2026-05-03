package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/robbyt/go-supervisor/internal/finitestate"
)

// Reload refreshes the server configuration and restarts the HTTP server if
// the config changed. The replacement server boots inside Run's event loop so
// its BaseContext (used for per-request contexts) is tied to runCtx rather
// than the caller's ctx — without this, cancelling ctx after Reload returned
// would cancel in-flight requests on the new server.
//
// The caller's ctx is admission-only: it cancels the request before it has
// been accepted by Run's loop. Once accepted, Reload waits for the restart to
// complete (or for the runner to stop) so the caller never observes the FSM
// in Reloading after Reload returns.
func (r *Runner) Reload(ctx context.Context) {
	logger := r.logger.WithGroup("Reload")

	// Fast-path: if the caller's ctx is already cancelled, bail before
	// invoking configCallback. Honors the "admission-only" contract
	// documented above.
	select {
	case <-ctx.Done():
		logger.Debug("Reload caller ctx done before dispatch", "error", ctx.Err())
		return
	default:
	}

	if err := r.fsm.TransitionIfCurrentState(
		finitestate.StatusRunning,
		finitestate.StatusReloading,
	); err != nil {
		logger.Debug("Skipping reload - not in Running",
			"current", r.fsm.GetState(), "error", err)
		return
	}

	newCfg, err := r.configCallback()
	if err != nil {
		logger.Error("config callback failed", "error", err)
		r.setStateError()
		return
	}
	if newCfg == nil {
		logger.Error("config callback returned nil")
		r.setStateError()
		return
	}
	if old := r.getConfig(); old != nil && newCfg.Equal(old) {
		logger.Debug("Config unchanged, skipping reload")
		if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
			logger.Error("Failed to transition from Reloading to Running", "error", err)
			r.setStateError()
		}
		return
	}

	if !r.canDispatchReload(ctx, logger) {
		if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
			logger.Error("Failed to transition from Reloading to Running", "error", err)
			r.setStateError()
		}
		return
	}

	req := &reloadReq{cfg: newCfg, done: make(chan struct{})}
	select {
	case r.reloadCh <- req:
		// Accepted. Wait for Run to finish the restart (or for the runner to
		// stop, in which case drainReloadCh closes req.done). Caller's ctx
		// is intentionally ignored here: returning while Run is still
		// restarting would let the caller observe FSM=Reloading after Reload
		// returned.
		<-req.done
	case <-ctx.Done():
		logger.Debug("Reload caller ctx done before dispatch", "error", ctx.Err())
		if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
			logger.Error("Failed to transition from Reloading to Running", "error", err)
			r.setStateError()
		}
	case <-r.lc.DoneCh():
		logger.Debug("Runner stopped before reload dispatch")
		if err := r.fsm.Transition(finitestate.StatusRunning); err != nil {
			logger.Error("Failed to transition from Reloading to Running", "error", err)
			r.setStateError()
		}
	}
}

func (r *Runner) canDispatchReload(ctx context.Context, logger *slog.Logger) bool {
	select {
	case <-ctx.Done():
		logger.Debug("Reload caller ctx done before dispatch", "error", ctx.Err())
		return false
	case <-r.lc.DoneCh():
		logger.Debug("Runner stopped before reload dispatch")
		return false
	default:
		return true
	}
}

// executeReload performs the actual restart sequence with ctx (= Run's runCtx).
// Called only from Run's event loop via handleReload.
func (r *Runner) executeReload(ctx context.Context, newCfg *Config) error {
	logger := r.logger.WithGroup("executeReload")
	logger.Debug("Reloading server with new config")

	r.mutex.Lock()
	defer r.mutex.Unlock()

	if err := r.stopServer(ctx); err != nil && !errors.Is(err, ErrServerNotRunning) {
		reloadErr := fmt.Errorf("failed to stop server during reload: %w", err)
		logger.Debug("Reload failed", "error", reloadErr)
		return reloadErr
	}

	r.setConfig(newCfg)

	if err := r.boot(ctx); err != nil {
		reloadErr := fmt.Errorf("failed to boot server during reload: %w", err)
		logger.Debug("Reload failed", "error", reloadErr)
		return reloadErr
	}
	logger.Debug("Reload completed")
	return nil
}
