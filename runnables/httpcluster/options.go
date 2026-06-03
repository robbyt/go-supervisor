package httpcluster

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/robbyt/go-supervisor/runnables/httpserver"
)

// Option is a function that configures a Runner.
type Option func(*Runner) error

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
// A buffer of 0 (default) makes the channel synchronous, providing natural backpressure
// and preventing rapid config updates that could cause server restart race conditions.
// Values > 1 may cause race conditions during heavy update pressure and are not recommended.
func WithSiphonBuffer(size int) Option {
	return func(r *Runner) error {
		if size > 1 {
			r.logger.Warn(
				"SiphonBuffer size > 1 may cause race conditions during heavy update pressure, keeping default 0 is recommended",
				"size",
				size,
			)
		}
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

// WithRestartDelay sets the delay between server restarts when configs change.
func WithRestartDelay(delay time.Duration) Option {
	return func(r *Runner) error {
		r.restartDelay = delay
		return nil
	}
}

// WithRestartBackoff configures the CrashLoopBackOff used when a backend
// crashes at runtime: the first restart waits initial, each subsequent restart
// doubles the wait, capped at max. Defaults are 100ms and 30s.
func WithRestartBackoff(initial, max time.Duration) Option {
	return func(r *Runner) error {
		if initial <= 0 {
			return fmt.Errorf("restart backoff initial must be positive: %v", initial)
		}
		if max < initial {
			return fmt.Errorf("restart backoff max (%v) must be >= initial (%v)", max, initial)
		}
		r.restartBackoffInitial = initial
		r.restartBackoffMax = max
		return nil
	}
}

// WithMaxRestarts sets the crash-loop threshold: a server that crashes more
// than max times within the restart window escalates the whole cluster to
// Error instead of being restarted again. Default is 5.
func WithMaxRestarts(max int) Option {
	return func(r *Runner) error {
		if max < 0 {
			return fmt.Errorf("max restarts cannot be negative: %d", max)
		}
		r.maxRestarts = max
		return nil
	}
}

// WithRestartWindow sets the sliding window over which crashes are counted
// against the max-restarts threshold. Default is 1 minute.
func WithRestartWindow(window time.Duration) Option {
	return func(r *Runner) error {
		if window <= 0 {
			return fmt.Errorf("restart window must be positive: %v", window)
		}
		r.restartWindow = window
		return nil
	}
}

// WithShutdownTimeout sets the deadline on the context that the cluster
// runner builds when it begins shutting down. Both the synchronous shutdown
// path and the background siphon drain observe that context, so the deadline
// cascades to every shutdown sub-task: when it fires, the drain exits via
// ctx.Done and any ctx-aware code inside the shutdown path unblocks. A
// timeout of 0 disables the deadline; the drain then exits only on quiescence
// (an internal inactivity window) or when the channel is closed. The default
// is 5 seconds, matching the supervisor's stateMonitorShutdownTimeout default.
func WithShutdownTimeout(d time.Duration) Option {
	return func(r *Runner) error {
		if d < 0 {
			return fmt.Errorf("shutdown timeout cannot be negative: %v", d)
		}
		r.shutdownTimeout = d
		return nil
	}
}
