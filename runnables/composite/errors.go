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
)
