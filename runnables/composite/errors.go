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

	// ErrConfigCallback wraps any error returned by ConfigCallback so callers
	// can react via errors.Is. The underlying callback error is preserved in
	// the chain via multi-%w wrapping.
	ErrConfigCallback = errors.New("failed to load configuration from callback")

	// ErrConfigCallbackNil is returned when ConfigCallback returns (nil, nil).
	ErrConfigCallbackNil = errors.New("config callback returned nil")

	// ErrReloadAbandoned is returned when a pending reload cannot
	// complete because the runner stopped first — either before the
	// reload request was accepted into Run's event loop, or while a
	// caller was waiting for the accepted request to finish.
	ErrReloadAbandoned = errors.New("reload abandoned because runner stopped")

	// ErrNilRunnable is returned by NewConfig / NewConfigFromRunnables
	// when a RunnableEntry's Runnable field is nil. Typically surfaces
	// from accidentally passing a typed-nil pointer (e.g.,
	// `var x *MyRunnable` left uninitialized) which satisfies the
	// interface constraint at compile time but blows up later inside
	// the lifecycle when any method is called on it.
	ErrNilRunnable = errors.New("entry has nil runnable")
)
