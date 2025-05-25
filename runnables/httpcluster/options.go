package httpcluster

import (
	"context"
	"log/slog"
)

// Option is a function that configures a Runner.
type Option func(*Runner) error

// WithContext sets the parent context for the cluster.
func WithContext(ctx context.Context) Option {
	return func(r *Runner) error {
		r.parentCtx = ctx
		return nil
	}
}

// WithLogger sets the logger for the cluster.
func WithLogger(logger *slog.Logger) Option {
	return func(r *Runner) error {
		r.logger = logger
		return nil
	}
}

// WithSiphonBuffer sets the buffer size for the configuration siphon channel.
// A buffer of 0 (default) makes the channel synchronous.
func WithSiphonBuffer(size int) Option {
	return func(r *Runner) error {
		r.siphonBuffer = size
		return nil
	}
}
