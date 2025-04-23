package composite

import (
	"context"
	"log/slog"
)

// Option represents a functional option for configuring CompositeRunner
type Option[T runnable] func(*Runner[T])

// WithLogHandler sets a custom slog handler for the CompositeRunner instance.
func WithLogHandler[T runnable](handler slog.Handler) Option[T] {
	return func(c *Runner[T]) {
		if handler != nil {
			c.logger = slog.New(handler.WithGroup("composite.Runner"))
		}
	}
}

// WithContext sets a custom context for the CompositeRunner instance.
// This allows for more granular control over cancellation and timeouts.
func WithContext[T runnable](ctx context.Context) Option[T] {
	return func(c *Runner[T]) {
		if ctx != nil {
			c.parentCtx, c.cancel = context.WithCancel(ctx)
		}
	}
}

// WithConfigCallback sets the function that will be called to load or reload configuration.
// This option is required when creating a new CompositeRunner.
func WithConfigCallback[T runnable](callback func() (*Config[T], error)) Option[T] {
	return func(c *Runner[T]) {
		c.configCallback = callback
	}
}
