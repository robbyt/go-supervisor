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
			c.parentCtx, c.parentCancel = context.WithCancel(ctx)
		}
	}
}
