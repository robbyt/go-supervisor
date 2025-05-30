package composite

import (
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
