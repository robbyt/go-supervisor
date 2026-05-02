package composite

import "errors"

var (
	// ErrCompositeRunnable is returned when there's a general error in the composite runnable
	ErrCompositeRunnable = errors.New("composite runnable error")

	// ErrRunnableFailed is returned when a child runnable fails
	ErrRunnableFailed = errors.New("child runnable failed")

	// ErrConfigMissing is returned when the config is missing
	ErrConfigMissing = errors.New("config is missing")

	// ErrOldConfig is returned when the config hasn't changed during a reload
	ErrOldConfig = errors.New("configuration unchanged")

	// ErrReloadAborted is returned when a Reload(ctx) call did not run to
	// completion as the result of normal control flow — caller's ctx
	// cancellation, runner shutting down, or the consumer observing the
	// caller's abandonment signal before starting work. Callers can use
	// errors.Is to distinguish this from real reload failures (config
	// callback errors, child stop/boot failures).
	//
	// IMPORTANT: only the dispatch layer (dispatchMembershipReload) and the
	// consumer abandonment path (reloadRequest.RunReload) may wrap this
	// sentinel. Operational failures from reloadWithRestart, boot, or
	// stopAllRunnables MUST return errors that do NOT wrap ErrReloadAborted —
	// otherwise Reload's triage will silently treat them as normal control
	// flow and skip setStateError().
	ErrReloadAborted = errors.New("reload aborted")
)
