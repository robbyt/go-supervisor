package httpcluster

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
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

// WithLogHandler sets the log handler for the cluster.
func WithLogHandler(handler slog.Handler) Option {
	return func(r *Runner) error {
		r.logger = slog.New(handler)
		return nil
	}
}

// WithSiphonBuffer sets the buffer size for the configuration siphon channel.
// A buffer of 0 (default) makes the channel synchronous.
func WithSiphonBuffer(size int) Option {
	return func(r *Runner) error {
		r.configSiphon = make(chan map[string]*httpserver.Config, size)
		return nil
	}
}

// WithCustomSiphonChannel sets the custom configuration siphon channel for the cluster.
func WithCustomSiphonChannel(channel chan map[string]*httpserver.Config) Option {
	return func(r *Runner) error {
		r.configSiphon = channel
		return nil
	}
}

// WithRunnerFactory sets the factory function for creating Runnable instances.
func WithRunnerFactory(
	factory runnerFactory,
) Option {
	return func(r *Runner) error {
		r.runnerFactory = factory
		return nil
	}
}

// WithStateChanBufferSize sets the buffer size for state channels.
// This helps prevent dropped state transitions in tests or when state changes happen rapidly.
// Default is 10. Size of 0 creates an unbuffered channel.
func WithStateChanBufferSize(size int) Option {
	return func(r *Runner) error {
		if size < 0 {
			return fmt.Errorf("state channel buffer size cannot be negative: %d", size)
		}
		r.stateChanBufferSize = size
		return nil
	}
}
