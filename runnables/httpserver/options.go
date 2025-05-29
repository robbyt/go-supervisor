package httpserver

import (
	"context"
	"log/slog"
)

// Option represents a functional option for configuring Runner.
type Option func(*Runner)

// WithLogHandler sets a custom slog handler for the Runner instance.
// For example, to use a custom JSON handler with debug level:
//
//	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
//	runner, err := httpserver.NewRunner(ctx, httpserver.WithConfigCallback(configCallback), httpserver.WithLogHandler(handler))
func WithLogHandler(handler slog.Handler) Option {
	return func(r *Runner) {
		if handler != nil {
			r.logger = slog.New(handler.WithGroup("httpserver.Runner"))
		}
	}
}

// WithContext sets a custom context for the Runner instance.
// This allows for more granular control over cancellation and timeouts.
func WithContext(ctx context.Context) Option {
	return func(r *Runner) {
		if ctx != nil {
			r.ctx, r.cancel = context.WithCancel(ctx)
		}
	}
}

// WithConfigCallback sets the function that will be called to load or reload configuration. Using
// this option or WithConfig is required to initialize the Runner instance, because it provides the
// configuration for the HTTP server managed by the Runner.
func WithConfigCallback(callback ConfigCallback) Option {
	return func(r *Runner) {
		r.configCallback = callback
	}
}

// WithConfig sets the initial configuration for the Runner instance.
// This option wraps the WithConfigCallback option, allowing you to pass a Config
// instance directly instead of a callback function. This is useful when you have a static
// configuration that doesn't require dynamic loading or reloading.
func WithConfig(cfg *Config) Option {
	return func(r *Runner) {
		callback := func() (*Config, error) {
			return cfg, nil
		}
		r.configCallback = callback
	}
}

// WithName sets the name of the Runner instance.
func WithName(name string) Option {
	return func(r *Runner) {
		r.name = name
	}
}
