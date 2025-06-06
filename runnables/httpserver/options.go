package httpserver

import (
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

// WithConfigCallback sets the function that will be called to load or reload configuration.
// Either this option or WithConfig initializes the Runner instance by providing the
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
