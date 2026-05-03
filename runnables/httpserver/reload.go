package httpserver

import (
	"context"
	"errors"
	"fmt"
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
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()

	logger := r.logger.WithGroup("Reload")

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
		return
	}

	req := &reloadReq{cfg: newCfg, done: make(chan struct{})}
	select {
	case r.reloadCh <- req:
		// Accepted. Wait for Run to finish the restart (or for the runner to
		// stop, in which case drainReloadCh closes req.done). Caller's ctx is
		// intentionally ignored here: returning while Run is still restarting
		// would let the caller observe FSM=Reloading after Reload returned.
		select {
		case <-req.done:
		case <-r.lc.DoneCh():
			logger.Debug("Runner stopped during reload")
		}
	case <-ctx.Done():
		logger.Debug("Reload caller ctx done before dispatch", "error", ctx.Err())
	case <-r.lc.DoneCh():
		logger.Debug("Runner stopped before reload dispatch")
	}
}

// executeReload performs the actual restart sequence with ctx (= Run's runCtx).
// Called only from Run's event loop via handleReload.
func (r *Runner) executeReload(ctx context.Context, newCfg *Config) error {
	logger := r.logger.WithGroup("executeReload")
	logger.Debug("Reloading server with new config")
	defer logger.Debug("Completed.")

	r.mutex.Lock()
	defer r.mutex.Unlock()

	if err := r.stopServer(ctx); err != nil && !errors.Is(err, ErrServerNotRunning) {
		return fmt.Errorf("failed to stop server during reload: %w", err)
	}

	r.setConfig(newCfg)

	if err := r.boot(ctx); err != nil {
		return fmt.Errorf("failed to boot server during reload: %w", err)
	}
	return nil
}
