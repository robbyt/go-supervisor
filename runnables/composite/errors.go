package composite

import "errors"

var (
	// ErrCompositeRunnable is returned when there's a general error in the composite runnable
	ErrCompositeRunnable = errors.New("composite runnable error")

	// ErrRunnableFailed is returned when a child runnable fails
	ErrRunnableFailed = errors.New("child runnable failed")

	// ErrConfigMissing is returned when the config is missing
	ErrConfigMissing = errors.New("config is missing")

	// ErrNoRunnables is returned when there are no runnables to manage
	ErrNoRunnables = errors.New("no runnables to manage")

	// ErrOldConfig is returned when the config hasn't changed during a reload
	ErrOldConfig = errors.New("configuration unchanged")
)
